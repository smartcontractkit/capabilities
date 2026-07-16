package trigger

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
)

const (
	SuffixLogTriggerFilterID     = "-evm-log-trigger"
	defaultSendChannelBufferSize = 1000
	defaultLimitQueryLogSize     = 1000
)

// cleanupInterval is how often stale log-poller filters are swept AND the
// minimum age a filter must reach before it is eligible for that sweep. The
// min-age guard closes a race the cleanup previously assumed away: a filter can
// be live at the log poller for a brief window before its store entry is
// observable, and cleaning it in that window would bill-then-kill a live
// filter.
const cleanupInterval = 30 * time.Second

// Metering identity constants for the EVM log trigger (SHARED-2711). These are
// the service-level dimensions of the base ResourceIdentity: Service is the
// stable service constant (it must not encode deployment environment or zone,
// which ride on the structured identity's coarse dimensions). Resource pool
// lives on ResourceIdentity; billing unit lives on Utilization.resource_type.
const (
	MeteringService      = "evm-log-trigger"
	MeteringResource     = "log_filters"
	MeteringResourceType = "log_filter_addresses"
)

// LogTriggerService is a resourcemanager.Meterable: it is registered with the
// ResourceManager at start so the manager's snapshot tick polls its active
// filters.
var _ resourcemanager.Meterable = (*LogTriggerService)(nil)

type LogTriggerService struct {
	services.Service

	baseTrigger *capabilities.BaseTriggerCapability[*evmcappb.Log]

	srvcEng *services.Engine

	EVMService        types.EVMService
	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder
	resourceManager   *resourcemanager.ResourceManager
	// baseIdentity is the producer's base metering identity: the six coarse
	// dimensions plus service/resource_pool, built once at Initialise.
	// Per-resource billing fields are set on Utilization.
	// When the host did not inject a capability DON ID, baseIdentity has an
	// empty DON identifier and is filled per-emit from the consumer's
	// WorkflowDonID.
	baseIdentity  resourcemanager.ResourceIdentity
	chainSelector string // decimal chain selector, the chain label on meter records
	// rmUnregister removes this service from the ResourceManager's snapshot
	// registry; it is set when the RM is started in start and invoked in close.
	rmUnregister func()

	triggers                        LogTriggerStore
	logTriggerPollInterval          time.Duration
	logTriggerSendChannelBufferSize uint64

	limitAndSort               query.LimitAndSort
	filterAddressLimiter       limits.BoundLimiter[int]
	filterTopicsPerSlotLimiter limits.BoundLimiter[int]
	eventRateLimit             limits.RateLimiter
	eventPayloadSizeLimiter    limits.BoundLimiter[commoncfg.Size]
	orgResolver orgresolver.OrgResolver // Optional org resolver for fetching organization IDs
	// filterRegisteredAt records, per log-poller filter name, the time it was
	// registered at the log poller. cleanUpStaleFilters uses it to skip filters
	// younger than one cleanup interval, closing the register-time window where
	// a filter is live at the poller before its store entry is observable.
	filterRegisteredAt sync.Map // filterID (string) -> time.Time
}

// NewLogTriggerService creates a new instance of logTriggerService.
func NewLogTriggerService(evmService types.EVMService, store LogTriggerStore, lggr logger.Logger, capabilityID string,
	beholderProcessor beholder.ProtoProcessor, messageBuilder *monitoring.MessageBuilder,
	logTriggerPollInterval time.Duration,
	logTriggerSendChannelBufferSize uint64,
	logTriggerLimitQueryLogSize uint64, limitsFactory limits.Factory,
	orgResolver orgresolver.OrgResolver,
	triggerEventStore capabilities.EventStore,
	resourceManager *resourcemanager.ResourceManager,
	baseIdentity resourcemanager.ResourceIdentity,
	chainSelector uint64) (*LogTriggerService, error) {
	if capabilityID == "" {
		return nil, fmt.Errorf("capabilityID must be non-empty")
	}
	if logTriggerPollInterval < 0 {
		return nil, fmt.Errorf("logTriggerPollInterval must be positive, got: %s", logTriggerPollInterval)
	}

	currentSendChannelBufferSize := uint64(defaultSendChannelBufferSize)
	if logTriggerSendChannelBufferSize != 0 {
		currentSendChannelBufferSize = logTriggerSendChannelBufferSize
	}
	currentLimitQueryLogSize := uint64(defaultLimitQueryLogSize)
	if logTriggerLimitQueryLogSize != 0 {
		if logTriggerLimitQueryLogSize > currentSendChannelBufferSize {
			return nil, fmt.Errorf("logTriggerLimitQueryLogSize (%d) must be less than logTriggerSendChannelBufferSize (%d)", logTriggerLimitQueryLogSize, currentSendChannelBufferSize)
		}
		currentLimitQueryLogSize = logTriggerLimitQueryLogSize
	}

	// all queries to log poller will have the same limit and sort, so we can create it once and reuse it
	limitAndSort := query.NewLimitAndSort(query.Limit{
		Count: currentLimitQueryLogSize,
	}, query.NewSortByBlock(query.Asc))

	lts := &LogTriggerService{
		EVMService:                      evmService,
		lggr:                            lggr,
		beholderProcessor:               beholderProcessor,
		messageBuilder:                  messageBuilder,
		resourceManager:                 resourceManager,
		baseIdentity:                    baseIdentity,
		chainSelector:                   strconv.FormatUint(chainSelector, 10),
		triggers:                        store,
		logTriggerPollInterval:          logTriggerPollInterval,
		logTriggerSendChannelBufferSize: currentSendChannelBufferSize,
		limitAndSort:                    limitAndSort,
		orgResolver:                     orgResolver,
	}
	if lts.orgResolver == nil {
		lts.lggr.Warn("OrgResolver is nil, EVM log trigger capability will not be able to fetch organization ID")
	}
	if lts.resourceManager == nil {
		lts.lggr.Warn("ResourceManager is nil, EVM log trigger capability will not emit meter records")
	}
	if err := lts.initLimiters(limitsFactory); err != nil {
		return nil, err
	}

	lts.Service, lts.srvcEng = services.Config{
		Name:  "EvmLogTriggerService",
		Start: lts.start,
		Close: lts.close,
	}.NewServiceEngine(lggr)

	if triggerEventStore == nil {
		return nil, fmt.Errorf("no trigger event store provided")
	}
	baseTrigger, err := capabilities.NewBaseTriggerCapabilityWithCRESettings(context.Background(), triggerEventStore,
		func() *evmcappb.Log { return &evmcappb.Log{} }, lts.lggr, capabilityID, limitsFactory.Settings)
	if err != nil {
		return nil, err
	}
	lts.baseTrigger = baseTrigger
	return lts, nil
}

func (lts *LogTriggerService) initLimiters(limitsFactory limits.Factory) (err error) {
	lts.filterAddressLimiter, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.LogTrigger.FilterAddressLimit)
	if err != nil {
		return
	}
	lts.filterTopicsPerSlotLimiter, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.LogTrigger.FilterTopicsPerSlotLimit)
	if err != nil {
		return
	}
	lts.eventRateLimit, err = limitsFactory.MakeRateLimiter(cresettings.Default.PerWorkflow.LogTrigger.EventRateLimit)
	if err != nil {
		return
	}
	lts.eventPayloadSizeLimiter, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.LogTrigger.EventSizeLimit)
	if err != nil {
		return
	}
	return
}

func (lts *LogTriggerService) start(ctx context.Context) error {
	err := lts.baseTrigger.Start(ctx)
	if err != nil {
		return err
	}
	ticker := services.NewTicker(cleanupInterval)
	lts.lggr.Infof("Starting clean up of failed log poller filters every %s", cleanupInterval)
	lts.srvcEng.GoTick(ticker, lts.cleanUpStaleFilters)

	// The ResourceManager owns the snapshot tick: start it as a sub-service of
	// this service and Register ourselves so its tick polls GetUtilization. We
	// never run our own snapshot loop. The RM is fail-open and starting it must
	// not gate the trigger service, so a start error is logged, not returned.
	if lts.resourceManager != nil {
		if err := lts.resourceManager.Start(ctx); err != nil {
			lts.lggr.Errorw("failed to start metering ResourceManager; snapshots disabled", "err", err)
		} else {
			lts.rmUnregister = lts.resourceManager.Register(lts)
		}
	}
	return nil
}

// close performs an orderly shutdown. There are NO process-lifecycle metering
// emissions: a graceful stop emits nothing, and billing releases each
// still-active filter by its absence from the next snapshot. The Meterable is
// deregistered from the ResourceManager FIRST so no snapshot tick can run
// against a half-torn-down service, then the base trigger, caching resolver,
// and ResourceManager are closed.
func (lts *LogTriggerService) close() error {
	if lts.rmUnregister != nil {
		lts.rmUnregister()
		lts.rmUnregister = nil
	}
	lts.baseTrigger.Stop()
	if lts.resourceManager != nil {
		return lts.resourceManager.Close()
	}
	return nil
}

func (lts *LogTriggerService) cleanUpStaleFilters(ctx context.Context) {
	lts.lggr.Debugf("Starting cleanUpStaleFilters")
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: capabilities.RequestMetadata{
		WorkflowID: "evm-log-trigger-cleanup", // fake workflow ID for monitoring purposes
	}}
	filterNames, err := lts.EVMService.GetFiltersNames(ctx)
	if err != nil {
		summary := fmt.Sprintf("failed to get the filter names: '%v'", err)
		monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerCleanUpError(telemetryContext, summary, err.Error()))
		return
	}
	toCleanUp := make(map[string]struct{})
	for _, filterName := range filterNames {
		//only add those that are from the log trigger
		if strings.HasSuffix(filterName, SuffixLogTriggerFilterID) {
			toCleanUp[filterName] = struct{}{}
		}
	}

	triggers := lts.triggers.ReadAll()
	for triggerID, state := range triggers {
		if _, exists := toCleanUp[state.filterID]; exists {
			delete(triggers, triggerID)
			delete(toCleanUp, state.filterID)
		}
	}

	lts.lggr.Debugf("Found %d filters to clean up that are not live", len(toCleanUp))

	for filterID := range toCleanUp {
		// Min-age guard: a filter that was registered at the log poller less
		// than one cleanup interval ago may simply not have its store entry
		// observable yet (the register path writes the store right after the
		// RegisterLogTracking RPC returns). Skip it this round so we never
		// bill-then-kill a filter that is actually live. Filters with no
		// recorded registration time (e.g. orphaned from a previous process)
		// are always eligible.
		if registeredAt, ok := lts.filterRegisteredAt.Load(filterID); ok {
			if time.Since(registeredAt.(time.Time)) < cleanupInterval {
				lts.lggr.Debugf("Skipping filter %s: younger than one cleanup interval", filterID)
				continue
			}
		}
		lts.lggr.Debugf("Cleaning up filter %s", filterID)
		if err := lts.EVMService.UnregisterLogTracking(ctx, filterID); err != nil {
			summary := fmt.Sprintf("failed to unregister log-tracking from the clean up thread: '%v' source triggerID: %s", err, filterID)
			monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerCleanUpError(telemetryContext, summary, err.Error()))
			continue
		}
		lts.filterRegisteredAt.Delete(filterID)
		// This is log-poller filter hygiene only; it emits no MeterRecord. An
		// orphaned filter has no trigger state, so it is already absent from
		// GetUtilization and therefore from subsequent Snapshots. Billing
		// reconciles the lost level by that absence (the snapshot liveness
		// mechanism), not by a synthetic cleanup emission.
	}
}

func (lts *LogTriggerService) RegisterLogTrigger(ctx context.Context, triggerID string, meta capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmcappb.Log], caperrors.Error) {
	lts.lggr.Infof("RegisterLogTrigger called with triggerID: %s, input: %+v", triggerID, input)
	ctx = meta.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	if triggerID == "" {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("no triggerID provided"), caperrors.Internal)
	}
	if _, exists := lts.triggers.Read(triggerID); exists {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("triggerID %q is already registered", triggerID), caperrors.Internal)
	}
	lenAddrs := len(input.GetAddresses())
	if lenAddrs == 0 {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("no valid addresses provided (at least one address is required)"), caperrors.InvalidArgument)
	}
	if err := lts.filterAddressLimiter.Check(ctx, lenAddrs); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}

	lenTopics := len(input.GetTopics())
	if lenTopics > 4 {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("there can be at most 4 topics provided, got %d instead", lenTopics), caperrors.InvalidArgument)
	}
	if lenTopics == 0 || len(input.GetTopics()[0].Values) == 0 {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("no valid event sig provided (at least one event sig is required in topics)"), caperrors.InvalidArgument)
	}
	for i, topic := range input.GetTopics() {
		if err := lts.filterTopicsPerSlotLimiter.Check(ctx, len(topic.Values)); err != nil {
			return nil, caperrors.NewPublicUserError(fmt.Errorf("topic %d: %w", i, err), caperrors.LimitExceeded)
		}
	}

	eventSigs, topics2, topics3, topics4 := lts.getTopics(input)
	lts.lggr.Debugw("RegisterLogTrigger input params", "addresses:", input.GetAddresses(), "eventSigs:", eventSigs, "topics2:", topics2, "topics3:", topics3, "topics4:", topics4, "confidence:", input.GetConfidence(), "triggerID:", triggerID)

	fromBlock, err := lts.getFinalizedBlockNumber(ctx, triggerID)
	if err != nil {
		return nil, caperrors.NewPrivateSystemError(err, caperrors.Unavailable)
	}

	filterID := lts.generateFilterID(triggerID)
	lts.lggr.Debugf("RegisterLogTracking id: %s", filterID)

	addresses, err := evmservice.ConvertAddressesFromProto(input.GetAddresses())
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("failed to convert addresses: %w", err), caperrors.InvalidArgument)
	}

	sigs, err := evmservice.ConvertHashesFromProto(eventSigs)
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("failed to convert eventSigs: %w", err), caperrors.InvalidArgument)
	}

	t2, err := evmservice.ConvertHashesFromProto(topics2)
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("failed to convert topics2: %w", err), caperrors.InvalidArgument)
	}

	t3, err := evmservice.ConvertHashesFromProto(topics3)
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("failed to convert topics3: %w", err), caperrors.InvalidArgument)
	}

	t4, err := evmservice.ConvertHashesFromProto(topics4)
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("failed to convert topics4: %w", err), caperrors.InvalidArgument)
	}

	filterQuery := evmtypes.LPFilterQuery{
		Name:      filterID,
		Addresses: addresses,
		EventSigs: sigs,
		Topic2:    t2,
		Topic3:    t3,
		Topic4:    t4,
	}

	expressions, confidence := lts.createLogRequest(ctx, addresses, sigs, t2, t3, t4, input.GetConfidence())

	// Build the filter's metering identity once from the already-converted
	// inputs: a workflow-independent content hash and the resolved DON ID. It is
	// stashed on the trigger state so every later path (unregister, cleanup,
	// snapshot) reproduces the same identity without the request input. The orgID
	// is resolved at registration and stored so emit/snapshot paths avoid network.
	var orgID string
	if lts.orgResolver != nil && meta.WorkflowOwner != "" {
		if resolved, err := lts.orgResolver.Get(ctx, meta.WorkflowOwner); err != nil {
			lts.lggr.Warnw("failed to resolve org ID for metering", "owner", meta.WorkflowOwner, "err", err)
		} else {
			orgID = resolved
		}
	}
	loggedFilter := filter{
		filterID:             filterID,
		physicalFilterID:     physicalFilterID(lts.chainSelector, addresses, sigs, t2, t3, t4),
		reservedAddressCount: int64(len(addresses)),
		donID:                lts.resolveDONID(meta.WorkflowDonID),
		workflowOwner:        meta.WorkflowOwner,
		orgID:                orgID,
		expressions:          expressions,
		confidence:           confidence,
	}

	if err = lts.EVMService.RegisterLogTracking(ctx, filterQuery); err != nil {
		registerError := fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s, addresses: %v, eventSig: %v, topic2: %v, topic3: %v, topic4: %v",
			err, triggerID, filterQuery.Addresses, filterQuery.EventSigs, filterQuery.Topic2, filterQuery.Topic3, filterQuery.Topic4)
		var lpError caperrors.Error
		if errors.As(err, &lpError) {
			if lpError.Origin() == caperrors.OriginUser {
				return nil, caperrors.NewPublicUserError(registerError, lpError.Code())
			}
		}

		summary := fmt.Sprintf("failed to register log-tracking: '%v' for triggerID: %s", err, triggerID)
		monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
		return nil, caperrors.NewPublicSystemError(registerError, caperrors.Unavailable)
	}
	// The filter is now live at the log poller. Record when so the stale-filter
	// cleanup skips it until it is at least one interval old (see
	// cleanUpStaleFilters).
	lts.filterRegisteredAt.Store(filterID, time.Now())

	// Create the polling context up front (cancelled on unregister or service
	// stop) so the store write is synchronous and carries a working cancelFunc.
	pollCtx, cancel := lts.srvcEng.NewCtx()

	// Write the trigger state SYNCHRONOUSLY, before emitting any delta, and let
	// the store report whether this is the physical filter's 0->1 activation.
	// Writing first means the orphan-cleanup thread can never observe the live
	// log-poller filter without its store entry and bill-then-kill it; deriving
	// the transition under the store lock keeps identical filters billed once.
	firstForPhysical := lts.triggers.WriteAndIsFirstForPhysical(triggerID, logTriggerState{
		cancelFunc:              cancel,
		lastBlock:               fromBlock,
		unfinalizedSentEventIDs: make(map[string]*big.Int),
		filter:                  loggedFilter,
	})
	if firstForPhysical {
		// 0->1 activation of a shared physical filter: bill +addressCount once.
		lts.emitDelta(ctx, loggedFilter.reservedAddressCount, "evm-activate", meta.WorkflowID, triggerID, loggedFilter)
	}

	monitoring.EmitInitiated(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerInitiated(telemetryContext, input))

	logCh := make(chan capabilities.TriggerAndId[*evmcappb.Log], lts.logTriggerSendChannelBufferSize)

	lts.baseTrigger.RegisterTrigger(triggerID, logCh)

	lts.srvcEng.GoCtx(pollCtx, func(ctx context.Context) {
		ctx = meta.ContextWithCRE(ctx)
		lts.startPolling(ctx, telemetryContext, triggerID, input, logCh)
	})

	return logCh, nil
}

func (lts *LogTriggerService) AckEvent(ctx context.Context, triggerID string, eventID string) caperrors.Error {
	if err := lts.baseTrigger.AckEvent(ctx, triggerID, eventID); err != nil {
		wrappedErr := fmt.Errorf("failed to AckEvent on baseTrigger (triggerID=%s eventID=%s): %w", triggerID, eventID, err)
		lts.lggr.Error(wrappedErr)
		return caperrors.NewPrivateSystemError(wrappedErr, caperrors.Internal)
	}
	return nil
}

func (lts *LogTriggerService) getTopics(input *evmcappb.FilterLogTriggerRequest) ([][]byte, [][]byte, [][]byte, [][]byte) {
	eventSigs := input.GetTopics()[0].Values
	var topics2, topics3, topics4 [][]byte
	if len(input.GetTopics()) > 1 && input.GetTopics()[1] != nil {
		topics2 = input.GetTopics()[1].Values
	}
	if len(input.GetTopics()) > 2 && input.GetTopics()[2] != nil {
		topics3 = input.GetTopics()[2].Values
	}
	if len(input.GetTopics()) > 3 && input.GetTopics()[3] != nil {
		topics4 = input.GetTopics()[3].Values
	}
	return eventSigs, topics2, topics3, topics4
}

func (lts *LogTriggerService) getFinalizedBlockNumber(ctx context.Context, triggerID string) (*big.Int, error) {
	reply, err := lts.EVMService.GetLatestLPBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to register latest and finalized log pollers block: '%w' for triggerID: %s", err, triggerID)
	}

	if reply == nil {
		return nil, fmt.Errorf("failed to register latest and finalized log pollers block: 'nil' for triggerID: %s", triggerID)
	}
	lts.lggr.Debugf("Latest finalized block number: %d, triggerID: %s", reply.FinalizedBlockNumber, triggerID)

	return big.NewInt(reply.FinalizedBlockNumber), nil
}

func (lts *LogTriggerService) generateFilterID(triggerID string) string {
	return triggerID + SuffixLogTriggerFilterID
}

// resolveDONID returns the base identity's DON ID. The DON ID is stamped on
// the identity at construction (via WithDonID) and is the single source of
// truth for metering and event labels.
func (lts *LogTriggerService) resolveDONID(workflowDonID uint32) string {
	return lts.baseIdentity.DonID()
}

// identity returns the base metering identity with DON ID resolved for one
// resource.
func (lts *LogTriggerService) identity(donID string) resourcemanager.ResourceIdentity {
	id := lts.baseIdentity
	if donID == "" {
		return id
	}
	id.Don = &resourcemanager.DonIdentity{
		DonID:  donID,
		NodeID: id.NodeID(),
	}
	return id
}

// emitDelta emits a signed delta MeterRecord (METER_ACTION_UPDATE) for a shared
// physical log filter: +addressCount on the physical filter's 0->1 activation,
// -addressCount on its 1->0 release. The physical filter content hash is the
// ResourceID, so all triggers sharing it bill against one resource. The org is
// resolved fresh from the stored owner at emit time. Emission is fail-open and
// must never gate the path that calls it.
//
// event_id is derived from the DON-aggregated request that drove the transition
// (workflowID + triggerID of the RegisterLogTrigger / UnregisterLogTrigger call),
// namespaced per action. The remote trigger publisher invokes those methods with
// the mode-aggregated request, byte-identical on every capability node, so the
// parts are DON-consistent. physicalFilterID is intentionally NOT the event_id
// (it stays the resource_id): it would collide across activate/release cycles.
func (lts *LogTriggerService) emitDelta(ctx context.Context, delta int64, namespace, workflowID, triggerID string, f filter) {
	if lts.resourceManager == nil {
		return
	}
	identity := lts.identity(f.donID)
	eventID := resourcemanager.EventID(namespace, workflowID, triggerID)
	lts.resourceManager.EmitDelta(ctx, identity, eventID, delta, resourcemanager.UtilizationFields{
		ResourceType: MeteringResourceType,
		ResourceID:   f.physicalFilterID,
		OrgID:        f.orgID,
	})
}

// ResourceIdentity implements resourcemanager.Meterable: it returns the
// producer's base identity (the six coarse dimensions plus
// service/resource_pool). The per-resource DON ID and billing fields are
// populated per active filter by GetUtilization.
func (lts *LogTriggerService) ResourceIdentity() resourcemanager.ResourceIdentity {
	return lts.baseIdentity
}

// GetUtilization implements resourcemanager.Meterable: it returns one snapshot
// entry per distinct physical filter (NOT one per trigger registration), since
// identical filters registered by many triggers share one billable physical
// resource. The value is the shared filter's address count. Org attribution for
// a shared filter uses the deterministic "lowest triggerID's owner" rule so all
// nodes agree on the same org without coordination. It is a cheap in-memory
// read — triggers.ReadAll already returns a copy — with no I/O and no lock held
// across the loop, and org is served from the caching resolver's memory, as the
// snapshot contract requires (R6).
func (lts *LogTriggerService) GetUtilization(ctx context.Context) []resourcemanager.SnapshotEntry {
	triggers := lts.triggers.ReadAll()

	// Dedup by physicalFilterID, keeping the filter owned by the lowest
	// triggerID for deterministic org attribution of a shared filter.
	type physicalAgg struct {
		f               filter
		lowestTriggerID string
	}
	byPhysical := make(map[string]*physicalAgg, len(triggers))
	for triggerID, state := range triggers {
		agg, ok := byPhysical[state.physicalFilterID]
		if !ok {
			byPhysical[state.physicalFilterID] = &physicalAgg{f: state.filter, lowestTriggerID: triggerID}
			continue
		}
		if triggerID < agg.lowestTriggerID {
			agg.lowestTriggerID = triggerID
			agg.f = state.filter
		}
	}

	entries := make([]resourcemanager.SnapshotEntry, 0, len(byPhysical))
	for _, agg := range byPhysical {
		f := agg.f
		orgID := f.orgID
		entries = append(entries, resourcemanager.SnapshotEntry{
			Identity: lts.identity(f.donID),
			Utilizations: []*meteringpb.Utilization{
				resourcemanager.NewUtilizationInt(f.reservedAddressCount, resourcemanager.UtilizationFields{
					ResourceType: MeteringResourceType,
					ResourceID:   f.physicalFilterID,
					OrgID:        orgID,
				}),
			},
		})
	}
	return entries
}

func (lts *LogTriggerService) startPolling(ctx context.Context, telemetryContext monitoring.TelemetryContext, triggerID string, input *evmcappb.FilterLogTriggerRequest, logCh chan capabilities.TriggerAndId[*evmcappb.Log]) {
	lts.lggr.Infof("Starting polling for triggerID: %s, interval: %d", triggerID, lts.logTriggerPollInterval)
	ticker := defaultTickerFactory.NewTicker(lts.logTriggerPollInterval)
	defer ticker.Stop()
	defer close(logCh)

	for {
		select {
		case <-ctx.Done():
			lts.lggr.Infof("Context cancelled for triggerID: %s, stopping polling", triggerID)
			return
		case <-ticker.Channel():
			state, exists := lts.triggers.Read(triggerID)
			if !exists {
				lts.lggr.Infof("Unregistered while polling triggerID: %s", triggerID)
				return
			}
			lts.lggr.Debugf("Awake, polling for triggerID: %s, currentOffset: %d", triggerID, state.lastBlock)
			logs, err := lts.fetchLogsFromLogPoller(ctx, state)
			if err != nil {
				summary := fmt.Sprintf("Failed to fetch logs for triggerID: %s, error: %v", triggerID, err)
				monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
				// no logs fetched, so we continue to the next iteration
				continue
			}

			finalizedBlockNumber, err := lts.getFinalizedBlockNumber(ctx, triggerID)
			if err != nil {
				summary := fmt.Sprintf("Failed to get latest finalized block number for triggerID: %s, error: %v", triggerID, err)
				monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
				// no finalized block number, so we continue to the next iteration
				continue
			}

			err = lts.sendLogsToWorkflows(ctx, telemetryContext, logs, finalizedBlockNumber, triggerID, state)
			if err != nil {
				summary := fmt.Sprintf("Failed to send logs for triggerID: %s, error: %v", triggerID, err)
				monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
				// serious error occurred while sending logs, so we break the loop
				return
			}

			lts.lggr.Debugf("Finished sending events for triggerID: %s, about to update latest block number (current offset: %d, latest finalized block: %d)", triggerID, state.lastBlock, finalizedBlockNumber)
			calculatedLatestBlock := lts.getLatestBlockNumber(logs, state.lastBlock, finalizedBlockNumber, triggerID)
			err = lts.triggers.Update(triggerID, calculatedLatestBlock, state.unfinalizedSentEventIDs)
			if err != nil {
				summary := fmt.Sprintf("Failed to update last block for triggerID: %s, error: %v", triggerID, err)
				monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
				// serious error occurred while updating the last processed block, so we break the loop
				return
			}
			successMessage := fmt.Sprintf("Finished updating BlockNumber for triggerID: %s, BlockNumber: %d, sent logs: %d", triggerID, calculatedLatestBlock, len(logs))
			monitoring.LogAndEmitSuccess(ctx, successMessage, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerSuccess(telemetryContext, triggerID, input, len(logs), calculatedLatestBlock.Int64()))
		}
	}
}

func (lts *LogTriggerService) sendLogsToWorkflows(ctx context.Context, telemetryContext monitoring.TelemetryContext,
	logs []*evmtypes.Log,
	finalizedBlockNumber *big.Int,
	triggerID string,
	trigger logTriggerState) error {
	lts.lggr.Debugf("Sending logs to workflow, triggerID: %s, finalizedBlockNumber: %d, logs size %d", triggerID, finalizedBlockNumber, len(logs))
	var needsUpdate bool
	sentCount := 0

	triggerIndex, err := workflows.GetTriggerIndexFromReferenceID(telemetryContext.ReferenceID)
	if err != nil {
		lts.lggr.Warnw("failed to get trigger index from reference ID", "err", err, "triggerID", triggerID, "workflowID", telemetryContext.WorkflowID, "refID", telemetryContext.ReferenceID)
		// continue with execution even if we can't get trigger index
		triggerIndex = 0
	}

	for _, log := range logs {
		if log == nil {
			lts.lggr.Errorf("Received nil log for triggerID: %s, skipping", triggerID)
			continue
		}

		eventID := lts.generateLogIdentifier(log)
		_, alreadySent := trigger.unfinalizedSentEventIDs[eventID]
		lts.lggr.Debugf("Working with triggerID: %s, eventID: %s, alreadySent: %t", triggerID, eventID, alreadySent)

		if alreadySent {
			continue
		}

		protoLog := evmcappb.ConvertLogToProto(*log)
		response := capabilities.TriggerAndId[*evmcappb.Log]{
			Id:      lts.generateLogIdentifier(log),
			Trigger: protoLog,
		}

		checksLimitsOk := lts.checkLimitsOnLog(ctx, telemetryContext, protoLog, triggerID, eventID, log)
		if !checksLimitsOk {
			continue
		}

		workflowExecutionID, execIDErr := workflows.GenerateExecutionIDWithTriggerIndex(telemetryContext.WorkflowID, response.Id, triggerIndex)
		if execIDErr != nil {
			lts.lggr.Errorw("failed to generate execution ID", "err", execIDErr, "isLegacyExecutionID", false, "triggerID", triggerID, "workflowID", telemetryContext.WorkflowID, "eventID", response.Id)
			// continue with execution even if we can't generate ID
			workflowExecutionID = ""
		}
		lts.lggr.Debugw("new log trigger event", "triggerEventID", response.Id, "triggerID", triggerID, "executionID", workflowExecutionID, "isLegacyExecutionID", false)

		displayWorkflowName := telemetryContext.DecodedWorkflowName
		if displayWorkflowName == "" {
			displayWorkflowName = telemetryContext.WorkflowName
		}
		labeler := custmsg.NewLabeler().With(
			events.KeyTriggerID, response.Id,
			events.KeyWorkflowID, telemetryContext.WorkflowID,
			events.KeyWorkflowExecutionID, workflowExecutionID,
			events.KeyWorkflowOwner, telemetryContext.WorkflowOwner,
			events.KeyWorkflowName, displayWorkflowName,
		)

		// Emit the *sending* capability DON ID (CRE-4409). resolveDONID applies
		// contract rule 8: the authoritative host-injected CapDONID (carried on
		// baseIdentity) wins, and the consumer workflow's WorkflowDonID is used
		// only when CapDONID is 0. This is the SAME resolver the metering
		// identity uses (filter.donID is set from resolveDONID at registration),
		// so the event label and the meter record cannot diverge.
		if donID := lts.resolveDONID(telemetryContext.WorkflowDonID); donID != "" {
			labeler = labeler.With(events.KeyDonID, donID)
		}
		if telemetryContext.WorkflowDonConfigVersion != 0 {
			labeler = labeler.With(events.KeyDonVersion, strconv.Itoa(int(telemetryContext.WorkflowDonConfigVersion)))
		}
		if telemetryContext.WorkflowRegistryChainSelector != "" {
			labeler = labeler.With(events.KeyWorkflowRegistryChainSelector, telemetryContext.WorkflowRegistryChainSelector)
		}
		if telemetryContext.WorkflowRegistryAddress != "" {
			labeler = labeler.With(events.KeyWorkflowRegistryAddress, telemetryContext.WorkflowRegistryAddress)
		}
		if telemetryContext.EngineVersion != "" {
			labeler = labeler.With(events.KeyEngineVersion, telemetryContext.EngineVersion)
		}

		// Try to fetch organization ID if org resolver is available
		if lts.orgResolver != nil && telemetryContext.WorkflowOwner != "" {
			if orgID, orgErr := lts.orgResolver.Get(ctx, telemetryContext.WorkflowOwner); orgErr != nil {
				lts.lggr.Warnw("Failed to fetch organization ID from org resolver", "workflowOwner", telemetryContext.WorkflowOwner, "error", orgErr, "triggerID", triggerID)
			} else if orgID != "" {
				labeler = labeler.With(events.KeyOrganizationID, orgID)
				lts.lggr.Debugw("Successfully fetched organization ID", "workflowOwner", telemetryContext.WorkflowOwner, "orgID", orgID, "triggerID", triggerID)
			}
		}

		if emitErr := events.EmitTriggerExecutionStarted(ctx, labeler); emitErr != nil {
			lts.lggr.Errorw("failed to emit trigger execution started event", "err", emitErr, "triggerID", triggerID, "workflowExecutionID", workflowExecutionID)
			// continue with execution even if event emission fails
		}

		lts.deliverLogReliably(ctx, telemetryContext, triggerID, protoLog, response.Id,
			finalizedBlockNumber, log, &trigger, &sentCount, &needsUpdate)
	}

	// Prune all entries in unfinalizedSentEventIds where the block number is less than or equal to finalizedBlockNumber
	for logID, blockNum := range trigger.unfinalizedSentEventIDs {
		if blockNum.Cmp(finalizedBlockNumber) <= 0 {
			delete(trigger.unfinalizedSentEventIDs, logID)
			needsUpdate = true
		}
	}

	if needsUpdate {
		err := lts.triggers.Update(triggerID, trigger.lastBlock, trigger.unfinalizedSentEventIDs)
		if err != nil {
			lts.lggr.Errorf("Failed to update unfinalized sent event IDs for triggerID: %s, error: %v", triggerID, err)
			return fmt.Errorf("failed to update unfinalized sent event IDs for triggerID: %s: %w", triggerID, err)
		}
	}
	lts.lggr.Debugf("Total logs successfully sent for triggerID: %s: %d (originally got: %d)", triggerID, sentCount, len(logs))
	return nil
}

// deliverLogReliably sends a single EVM log to the BaseTriggerCapability
// for persistence, retransmission, and ACKing.
func (lts *LogTriggerService) deliverLogReliably(
	ctx context.Context,
	telemetryContext monitoring.TelemetryContext,
	triggerID string,
	protoLog *evmcappb.Log,
	eventID string,
	finalizedBlockNumber *big.Int,
	log *evmtypes.Log,
	trigger *logTriggerState,
	sentCount *int,
	needsUpdate *bool,
) {
	anyPayload, err := anypb.New(protoLog)
	if err != nil {
		lts.lggr.Errorw("failed to pack protoLog into Any",
			"err", err, "triggerID", triggerID, "eventID", eventID)
		return
	}

	te := capabilities.TriggerEvent{
		TriggerType: triggerID,
		ID:          eventID,
		Payload:     anyPayload,
	}

	lts.lggr.Infow("Sending log event to pipe", "triggerID", triggerID, "eventID", eventID, "blockNumber", log.BlockNumber, "txHash", log.TxHash)
	deliverCtx := capcommon.ContextWithOrgForDelivery(ctx, lts.lggr, lts.orgResolver, telemetryContext.RequestMetadata)
	if err := lts.baseTrigger.DeliverEvent(deliverCtx, te, triggerID); err != nil {
		summary := fmt.Sprintf("failed to persist/deliver event (triggerID=%s, eventID=%s): %v", triggerID, eventID, err)
		lts.lggr.Error(summary)
		monitoring.LogAndEmitError(
			ctx,
			lts.lggr,
			lts.beholderProcessor,
			lts.messageBuilder.BuildLogTriggerEventDroppedError(telemetryContext, triggerID, log, summary, err.Error(), false),
		)
		return
	}

	// Once persisted, consider it "sent" from trigger’s POV (BaseTriggerCapability handles retries/ACK/lost)
	*sentCount++
	if log.BlockNumber.Cmp(finalizedBlockNumber) > 0 {
		trigger.unfinalizedSentEventIDs[eventID] = log.BlockNumber
		*needsUpdate = true
	}
}

// checkLimitsOnLog checks the rate limit and payload size limit for a single log event, it should not error as we
// want to continue processing other logs even if one fails the limits
func (lts *LogTriggerService) checkLimitsOnLog(ctx context.Context, telemetryContext monitoring.TelemetryContext, protoLog *evmcappb.Log, triggerID string, eventID string, log *evmtypes.Log) bool {
	if err := lts.eventRateLimit.AllowErr(ctx); err != nil {
		summary := fmt.Sprintf("Rate limited, dropping event (triggerID: %s, eventID: %s)", triggerID, eventID)
		lts.lggr.Errorw(summary, "triggerID", triggerID, "eventID", eventID, "err", err)
		monitoring.LogAndEmitError(
			ctx,
			lts.lggr,
			lts.beholderProcessor,
			lts.messageBuilder.BuildLogTriggerEventDroppedError(telemetryContext, triggerID, log, summary, err.Error(), true),
		)
		return false
	}

	protoLogSize := commoncfg.Size(proto.Size(protoLog))
	if err := lts.eventPayloadSizeLimiter.Check(ctx, protoLogSize); err != nil {
		summary := fmt.Sprintf("Size limited, log size is too big (current size %d), dropping event (triggerID: %s, eventID: %s)", protoLogSize, triggerID, eventID)
		lts.lggr.Errorw(summary, "triggerID", triggerID, "eventID", eventID, "protoLogSize", protoLogSize, "err", err)
		monitoring.LogAndEmitError(
			ctx,
			lts.lggr,
			lts.beholderProcessor,
			lts.messageBuilder.BuildLogTriggerEventDroppedError(telemetryContext, triggerID, log, summary, err.Error(), true),
		)
		return false
	}

	// all checks passed, no limit fired
	return true
}

// generateLogIdentifier creates the trigger event id, a unique identifier for the log based on its transaction hash, block hash, and index
func (lts *LogTriggerService) generateLogIdentifier(log *evmtypes.Log) string {
	return fmt.Sprintf("%x:%x:%d", log.TxHash, log.BlockHash, log.LogIndex)
}

func (lts *LogTriggerService) getLatestBlockNumber(logs []*evmtypes.Log, currentBlockNumber *big.Int, finalizedBlockNumber *big.Int, triggerID string) *big.Int {
	result := currentBlockNumber
	for _, l := range logs {
		// it has to iterate over all logs to update the last block number, as it could be multiple addresses with different block numbers among them
		blockNumber := l.BlockNumber
		if blockNumber.Cmp(result) > 0 && blockNumber.Cmp(finalizedBlockNumber) <= 0 {
			result = blockNumber
		}
	}

	lts.lggr.Debugf("getLatestBlockNumber result: %d (logs size: %d, currentBlockNumber: %d, finalizedBlockNumber: %d, triggerID: %s)",
		result,
		len(logs), currentBlockNumber, finalizedBlockNumber, triggerID)

	return result
}

func (lts *LogTriggerService) fetchLogsFromLogPoller(ctx context.Context, triggerState logTriggerState) ([]*evmtypes.Log, error) {
	block := fmt.Sprintf("%d", triggerState.lastBlock)
	expressions := append(triggerState.expressions, query.Block(block, primitives.Gt))
	logs, err := lts.EVMService.QueryTrackedLogs(ctx, expressions, lts.limitAndSort, triggerState.confidence)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs: %w", err)
	}
	return logs, nil
}

func (lts *LogTriggerService) createLogRequest(_ context.Context, addresses []evmtypes.Address, eventSigs, topics2, topics3, topics4 []evmtypes.Hash, confidence evmcappb.ConfidenceLevel) ([]query.Expression, primitives.ConfidenceLevel) {
	var expressions []query.Expression

	var addressFilters []query.Expression
	for _, addr := range addresses {
		addressFilters = append(addressFilters, evm.NewAddressFilter(addr))
	}
	expressions = append(expressions, query.Or(addressFilters...))

	var topicFilters []query.Expression
	for _, topic := range eventSigs {
		topicFilters = append(topicFilters, evm.NewEventSigFilter(topic))
	}
	expressions = append(expressions, query.Or(topicFilters...))

	var confidenceLevel primitives.ConfidenceLevel
	switch confidence {
	case evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_FINALIZED:
		confidenceLevel = primitives.Finalized
	case evmcappb.ConfidenceLevel_CONFIDENCE_LEVEL_LATEST:
		confidenceLevel = primitives.Unconfirmed
	default:
		//Default ConfidenceLevel_CONFIDENCE_LEVEL_SAFE
		confidenceLevel = primitives.Safe
	}

	for i, t := range [][]evmtypes.Hash{topics2, topics3, topics4} {
		// G115: integer overflow conversion uint64 -> int64 (gosec)
		// nolint:gosec
		topic := uint64(i + 1)
		topicExpression := lts.makeEventByTopicFilter(topic, t)
		if topicExpression == nil {
			continue
		}
		expressions = append(expressions, *topicExpression)
	}

	return expressions, confidenceLevel
}

func (lts *LogTriggerService) makeEventByTopicFilter(topicIndex uint64, topics []evmtypes.Hash) *query.Expression {
	if len(topics) == 0 {
		return nil
	}
	var singleTopicFilters []query.Expression
	for _, topic := range topics {
		tf := evm.NewEventByTopicFilter(topicIndex, []evm.HashedValueComparator{{
			Values:   []evmtypes.Hash{topic},
			Operator: primitives.Eq,
		}})
		singleTopicFilters = append(singleTopicFilters, tf)
	}
	orExpression := query.Or(singleTopicFilters...)
	return &orExpression
}

func (lts *LogTriggerService) UnregisterLogTrigger(ctx context.Context, triggerID string, meta capabilities.RequestMetadata, _ *evmcappb.FilterLogTriggerRequest) caperrors.Error {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	if triggerID == "" {
		return caperrors.NewPublicSystemError(fmt.Errorf("no triggerID provided"), caperrors.Internal)
	}
	trigger, found := lts.triggers.Read(triggerID)
	if !found {
		return caperrors.NewPublicSystemError(fmt.Errorf("no active trigger found for triggerID: %s", triggerID), caperrors.NotFound)
	}
	lts.lggr.Infof("UnregisterLogTrigger triggerID: %s", triggerID)
	trigger.cancelFunc()
	// Delete under the store lock and learn whether this was the physical
	// filter's 1->0 release (no other trigger still holds it). Only that
	// transition bills a -delta, mirroring the +delta billed on 0->1 activation.
	_, lastForPhysical := lts.triggers.DeleteAndIsLastForPhysical(triggerID, trigger.physicalFilterID)
	lts.filterRegisteredAt.Delete(lts.generateFilterID(triggerID))
	lts.baseTrigger.UnregisterTrigger(triggerID)
	if lastForPhysical {
		// 1->0 release of the shared physical filter: bill -addressCount once.
		// The value and identity are reused from the stashed filter so this
		// -delta reverses the exact +delta the activation billed.
		lts.emitDelta(ctx, -trigger.reservedAddressCount, "evm-release", meta.WorkflowID, triggerID, trigger.filter)
	}

	err := lts.EVMService.UnregisterLogTracking(ctx, lts.generateFilterID(triggerID))
	if err != nil {
		summary := fmt.Sprintf("failed to unregister log-tracking: '%v' for triggerID: %s", err, triggerID)
		monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
		unregisterLogTrackingError := fmt.Errorf("failed to unregister log-tracking: '%w' for triggerID: %s", err, triggerID)
		return caperrors.NewPrivateSystemError(unregisterLogTrackingError, caperrors.Unavailable)
	}
	return nil
}

// tickerFactory allows to mock time.Ticker in tests.
type tickerFactory interface {
	NewTicker(time.Duration) tickerWrapper
}

type tickerWrapper interface {
	Channel() <-chan time.Time
	Stop()
}
type realTickerFactory struct{}

func (realTickerFactory) NewTicker(d time.Duration) tickerWrapper {
	return realTicker{time.NewTicker(d)}
}

type realTicker struct {
	t *time.Ticker
}

func (r realTicker) Channel() <-chan time.Time {
	return r.t.C
}

func (r realTicker) Stop() {
	r.t.Stop()
}

var defaultTickerFactory tickerFactory = realTickerFactory{}
