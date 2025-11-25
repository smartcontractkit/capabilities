package trigger

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
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

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
)

const (
	SuffixLogTriggerFilterID     = "-evm-log-trigger"
	defaultSendChannelBufferSize = 1000
	defaultLimitQueryLogSize     = 1000
)

type LogTriggerService struct {
	services.Service
	srvcEng *services.Engine

	EVMService        types.EVMService
	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder

	triggers                        LogTriggerStore
	logTriggerPollInterval          time.Duration
	logTriggerSendChannelBufferSize uint64

	limitAndSort               query.LimitAndSort
	filterAddressLimiter       limits.BoundLimiter[int]
	filterTopicsPerSlotLimiter limits.BoundLimiter[int]
	eventRateLimit             limits.RateLimiter
	eventPayloadSizeLimiter    limits.BoundLimiter[commoncfg.Size]
	orgResolver                orgresolver.OrgResolver // Optional org resolver for fetching organization IDs
}

// NewLogTriggerService creates a new instance of logTriggerService.
// TODO PLEX-1465: the core logic of RegisterLogTrigger/UnregisterLogTrigger/Close/etc. should be moved to the EVMService, so it can be used by other services as well.
func NewLogTriggerService(evmService types.EVMService, store LogTriggerStore, lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor, messageBuilder *monitoring.MessageBuilder,
	logTriggerPollInterval time.Duration,
	logTriggerSendChannelBufferSize uint64,
	logTriggerLimitQueryLogSize uint64, limitsFactory limits.Factory,
	orgResolver orgresolver.OrgResolver) (*LogTriggerService, error) {
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
		triggers:                        store,
		logTriggerPollInterval:          logTriggerPollInterval,
		logTriggerSendChannelBufferSize: currentSendChannelBufferSize,
		limitAndSort:                    limitAndSort,
		orgResolver:                     orgResolver,
	}
	if lts.orgResolver == nil {
		lts.lggr.Warn("OrgResolver is nil, EVM log trigger capability will not be able to fetch organization ID")
	}
	if err := lts.initLimiters(limitsFactory); err != nil {
		return nil, err
	}

	lts.Service, lts.srvcEng = services.Config{
		Name:  "EvmLogTriggerService",
		Start: lts.start,
	}.NewServiceEngine(lggr)

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
	return
}

func (lts *LogTriggerService) start(_ context.Context) error {
	duration := 30 * time.Second
	ticker := services.NewTicker(duration)
	lts.lggr.Debugf("Starting clean up of failed log poller filters every %s seconds", duration)
	lts.srvcEng.GoTick(ticker, lts.cleanUpStaleFilters)
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
		lts.lggr.Debugf("Cleaning up filter %s", filterID)
		if err := lts.EVMService.UnregisterLogTracking(ctx, filterID); err != nil {
			summary := fmt.Sprintf("failed to unregister log-tracking from the clean up thread: '%v' source triggerID: %s", err, filterID)
			monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerCleanUpError(telemetryContext, summary, err.Error()))
		}
	}
}

func (lts *LogTriggerService) RegisterLogTrigger(ctx context.Context, triggerID string, meta capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmcappb.Log], error) {
	lts.lggr.Debugf("RegisterLogTrigger called with triggerID: %s, input: %+v", triggerID, input)
	ctx = meta.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	if triggerID == "" {
		return nil, actions.EnsureRemoteReportable(fmt.Errorf("no triggerID provided"))
	}
	if _, exists := lts.triggers.Read(triggerID); exists {
		return nil, actions.EnsureRemoteReportable(fmt.Errorf("triggerID %q is already registered", triggerID))
	}
	lenAddrs := len(input.GetAddresses())
	if lenAddrs == 0 {
		return nil, actions.NewUserError(fmt.Errorf("no valid addresses provided (at least one address is required)"))
	}
	if err := lts.filterAddressLimiter.Check(ctx, lenAddrs); err != nil {
		return nil, actions.NewUserError(err)
	}

	lenTopics := len(input.GetTopics())
	if lenTopics > 4 {
		return nil, actions.NewUserError(fmt.Errorf("there can be at most 4 topics provided, got %d instead", lenTopics))
	}
	if lenTopics == 0 || len(input.GetTopics()[0].Values) == 0 {
		return nil, actions.NewUserError(fmt.Errorf("no valid event sig provided (at least one event sig is required in topics)"))
	}
	for i, topic := range input.GetTopics() {
		if err := lts.filterTopicsPerSlotLimiter.Check(ctx, len(topic.Values)); err != nil {
			return nil, actions.NewUserError(fmt.Errorf("topic %d: %w", i, err))
		}
	}

	eventSigs, topics2, topics3, topics4 := lts.getTopics(input)
	lts.lggr.Debugw("RegisterLogTrigger input params", "addresses:", input.GetAddresses(), "eventSigs:", eventSigs, "topics2:", topics2, "topics3:", topics3, "topics4:", topics4, "confidence:", input.GetConfidence(), "triggerID:", triggerID)

	fromBlock, err := lts.getFinalizedBlockNumber(ctx, triggerID)
	if err != nil {
		return nil, actions.EnsureRemoteReportable(err)
	}

	filterID := lts.generateFilterID(triggerID)
	lts.lggr.Debugf("RegisterLogTracking id: %s", filterID)

	addresses, err := evmservice.ConvertAddressesFromProto(input.GetAddresses())
	if err != nil {
		return nil, actions.NewUserError(fmt.Errorf("failed to convert addresses: %w", err))
	}

	sigs, err := evmservice.ConvertHashesFromProto(eventSigs)
	if err != nil {
		return nil, actions.NewUserError(fmt.Errorf("failed to convert eventSigs: %w", err))
	}

	t2, err := evmservice.ConvertHashesFromProto(topics2)
	if err != nil {
		return nil, actions.NewUserError(fmt.Errorf("failed to convert topics2: %w", err))
	}

	t3, err := evmservice.ConvertHashesFromProto(topics3)
	if err != nil {
		return nil, actions.NewUserError(fmt.Errorf("failed to convert topics3: %w", err))
	}

	t4, err := evmservice.ConvertHashesFromProto(topics4)
	if err != nil {
		return nil, actions.NewUserError(fmt.Errorf("failed to convert topics4: %w", err))
	}

	filterQuery := evmtypes.LPFilterQuery{
		Name:      filterID,
		Addresses: addresses,
		EventSigs: sigs,
		Topic2:    t2,
		Topic3:    t3,
		Topic4:    t4,
	}

	if err = lts.EVMService.RegisterLogTracking(ctx, filterQuery); err != nil {
		return nil, actions.EnsureRemoteReportable(fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s, addresses: %v, eventSig: %v, topic2: %v, topic3: %v, topic4: %v",
			err, triggerID, filterQuery.Addresses, filterQuery.EventSigs, filterQuery.Topic2, filterQuery.Topic3, filterQuery.Topic4))
	}
	expressions, confidence := lts.createLogRequest(ctx, addresses, sigs, t2, t3, t4, input.GetConfidence())

	monitoring.EmitInitiated(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerInitiated(telemetryContext, input))

	logCh := make(chan capabilities.TriggerAndId[*evmcappb.Log], lts.logTriggerSendChannelBufferSize)
	lts.srvcEng.Go(func(ctx context.Context) {
		ctx, cancel := context.WithCancel(ctx)
		lts.triggers.Write(triggerID, logTriggerState{
			cancelFunc:              cancel,
			lastBlock:               fromBlock,
			unfinalizedSentEventIDs: make(map[string]*big.Int),
			filter: filter{
				filterID:    filterID,
				expressions: expressions,
				confidence:  confidence,
			},
		})
		ctx = meta.ContextWithCRE(ctx)
		lts.startPolling(ctx, telemetryContext, triggerID, input, logCh)
	})

	return logCh, nil
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

func (lts *LogTriggerService) startPolling(ctx context.Context, telemetryContext monitoring.TelemetryContext, triggerID string, input *evmcappb.FilterLogTriggerRequest, logCh chan capabilities.TriggerAndId[*evmcappb.Log]) {
	lts.lggr.Debugf("Starting polling for triggerID: %s, interval: %d", triggerID, lts.logTriggerPollInterval)
	ticker := defaultTickerFactory.NewTicker(lts.logTriggerPollInterval)
	defer ticker.Stop()
	defer close(logCh)

	for {
		select {
		case <-ctx.Done():
			lts.lggr.Debugf("Context cancelled for triggerID: %s, stopping polling", triggerID)
			return
		case <-ticker.Channel():
			state, exists := lts.triggers.Read(triggerID)
			if !exists {
				lts.lggr.Debugf("Unregistered while polling triggerID: %s", triggerID)
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

			err = lts.sendLogsToWorkflows(ctx, telemetryContext, logs, finalizedBlockNumber, triggerID, state, logCh)
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
	trigger logTriggerState,
	logCh chan capabilities.TriggerAndId[*evmcappb.Log]) error {
	lts.lggr.Debugf("Sending logs to workflow, triggerID: %s, finalizedBlockNumber: %d, logs size %d", triggerID, finalizedBlockNumber, len(logs))
	var needsUpdate bool
	sentCount := 0

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

		workflowExecutionID, err := workflows.EncodeExecutionID(telemetryContext.RequestMetadata.WorkflowID, response.Id)
		if err != nil {
			lts.lggr.Errorw("failed to generate execution ID", "err", err, "triggerID", triggerID, "workflowID", telemetryContext.RequestMetadata.WorkflowID, "eventID", response.Id)
			// continue with execution even if we can't generate ID
			workflowExecutionID = ""
		}

		labeler := custmsg.NewLabeler().With(
			events.KeyTriggerID, response.Id,
			events.KeyWorkflowID, telemetryContext.RequestMetadata.WorkflowID,
			events.KeyWorkflowExecutionID, workflowExecutionID,
			events.KeyWorkflowOwner, telemetryContext.RequestMetadata.WorkflowOwner,
			events.KeyWorkflowName, telemetryContext.RequestMetadata.WorkflowName,
		)

		// add DON metadata if available
		if telemetryContext.RequestMetadata.WorkflowDonID != 0 {
			labeler = labeler.With(events.KeyDonID, strconv.Itoa(int(telemetryContext.RequestMetadata.WorkflowDonID)))
		}
		if telemetryContext.RequestMetadata.WorkflowDonConfigVersion != 0 {
			labeler = labeler.With(events.KeyDonVersion, strconv.Itoa(int(telemetryContext.RequestMetadata.WorkflowDonConfigVersion)))
		}
		if telemetryContext.RequestMetadata.WorkflowRegistryChainSelector != "" {
			labeler = labeler.With(events.KeyWorkflowRegistryChainSelector, telemetryContext.RequestMetadata.WorkflowRegistryChainSelector)
		}
		if telemetryContext.RequestMetadata.WorkflowRegistryAddress != "" {
			labeler = labeler.With(events.KeyWorkflowRegistryAddress, telemetryContext.RequestMetadata.WorkflowRegistryAddress)
		}
		if telemetryContext.RequestMetadata.EngineVersion != "" {
			labeler = labeler.With(events.KeyEngineVersion, telemetryContext.RequestMetadata.EngineVersion)
		}

		// Try to fetch organization ID if org resolver is available
		if lts.orgResolver != nil && telemetryContext.RequestMetadata.WorkflowOwner != "" {
			if orgID, orgErr := lts.orgResolver.Get(ctx, telemetryContext.RequestMetadata.WorkflowOwner); orgErr != nil {
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

		select {
		case logCh <- response:
			sentCount++
			if log.BlockNumber.Cmp(finalizedBlockNumber) > 0 {
				// log's block number is unfinalized and needs to be tracked
				trigger.unfinalizedSentEventIDs[eventID] = log.BlockNumber
				needsUpdate = true
			}
		default:
			summary := fmt.Sprintf("Callback channel full (buffer size: %d), dropping event (triggerID: %s, eventID: %s)", lts.logTriggerSendChannelBufferSize, triggerID, response.Id)
			lts.lggr.Errorw(summary, "triggerID", triggerID, "eventID", response.Id)
			monitoring.LogAndEmitError(
				ctx,
				lts.lggr,
				lts.beholderProcessor,
				lts.messageBuilder.BuildLogTriggerEventDroppedError(telemetryContext, triggerID, log, summary, summary, false),
			)
		}
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

func (lts *LogTriggerService) UnregisterLogTrigger(ctx context.Context, triggerID string, meta capabilities.RequestMetadata, _ *evmcappb.FilterLogTriggerRequest) error {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	if triggerID == "" {
		return caperrors.NewPublicSystemError(fmt.Errorf("no triggerID provided"), caperrors.Unknown)
	}
	trigger, found := lts.triggers.Read(triggerID)
	if !found {
		return caperrors.NewPublicSystemError(fmt.Errorf("no active trigger found for triggerID: %s", triggerID), caperrors.Unknown)
	}
	lts.lggr.Debugf("UnregisterLogTrigger triggerID: %s", triggerID)
	trigger.cancelFunc()
	lts.triggers.Delete(triggerID)

	err := lts.EVMService.UnregisterLogTracking(ctx, lts.generateFilterID(triggerID))
	if err != nil {
		summary := fmt.Sprintf("failed to unregister log-tracking: '%v' for triggerID: %s", err, triggerID)
		monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(telemetryContext, triggerID, summary, err.Error()))
		return caperrors.NewPublicSystemError(fmt.Errorf("failed to unregister log-tracking: '%w' for triggerID: %s", err, triggerID), caperrors.Unknown)
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
