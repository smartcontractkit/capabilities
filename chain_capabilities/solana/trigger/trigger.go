package trigger

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solanacappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	solprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/solana"

	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
)

const (
	SuffixLogTriggerFilterID = "-solana-log-trigger"
	defaultQueryLimit        = 1000
)

func validateFilterConfig(config *solanacappb.FilterLogTriggerRequest) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if len(config.Address) != 32 {
		return fmt.Errorf("invalid address length: expected 32 bytes, got %d", len(config.Address))
	}
	if config.EventName == "" {
		return fmt.Errorf("event name cannot be empty")
	}
	if config.Name == "" {
		return fmt.Errorf("filter name cannot be empty")
	}
	if len(config.ContractIdlJson) == 0 {
		return fmt.Errorf("event idl json cannot be empty")
	}
	return nil
}

func newUserError(err error) caperrors.Error {
	return caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
}

func (lts *SolanaLogTriggerService) ToLogPollerFilter(triggerID string, config *solanacappb.FilterLogTriggerRequest) (*solana.LPFilterQuery, error) {
	var address solana.PublicKey
	if len(config.Address) != len(address) {
		return nil, fmt.Errorf("invalid address length: expected %d bytes, got %d", len(address), len(config.Address))
	}
	copy(address[:], config.Address)

	var cpiFilterConfig *solana.CPIFilterConfig
	if config.CpiFilterConfig != nil {
		cpiFilterConfig = &solana.CPIFilterConfig{
			DestAddress: address,
			MethodName:  string(config.CpiFilterConfig.MethodName),
		}
	}

	return &solana.LPFilterQuery{
		Name:            lts.generateFilterID(triggerID),
		Address:         address,
		EventName:       config.EventName,
		EventSig:        getEventSig(config.EventName),
		ContractIdlJSON: config.ContractIdlJson,
		SubkeyPaths:     getSubkeyPaths(config.Subkeys),
		Retention:       lts.retention,
		MaxLogsKept:     lts.maxLogsKept,
		IncludeReverted: true,
		CPIFilterConfig: cpiFilterConfig,
	}, nil
}

// BuildQueryExpressions builds query expressions including subkey filters
func BuildQueryExpressions(config *solanacappb.FilterLogTriggerRequest, lastProcessedBlock int64) ([]query.Expression, error) {
	expressions := []query.Expression{
		solprimitives.NewAddressFilter(solana.PublicKey(config.Address)),
		solprimitives.NewEventSigFilter(getEventSig(config.EventName)),
	}

	if lastProcessedBlock >= 0 {
		blockStr := strconv.FormatInt(lastProcessedBlock, 10)
		expressions = append(expressions, query.Block(blockStr, primitives.Gt))
	}

	for i, subkey := range config.Subkeys {
		if subkey != nil && len(subkey.Comparers) > 0 {
			comparers, err := solanacappb.ConvertValueComparatorsFromProto(subkey.Comparers)
			if err != nil {
				return nil, fmt.Errorf("failed to convert value comparators: %w", err)
			}
			indexedComparers := make([]solprimitives.IndexedValueComparator, len(comparers))
			for j, comp := range comparers {
				value, ok := comp.Value.([]byte)
				if !ok {
					return nil, fmt.Errorf("failed to convert comparer value to []byte for subkey index %d", i)
				}
				indexedComparers[j] = solprimitives.IndexedValueComparator{
					Value:    value,
					Operator: comp.Operator,
				}
			}
			subkeyExpr := solprimitives.NewEventBySubkeyFilter(uint64(i), indexedComparers)
			expressions = append(expressions, subkeyExpr)
		}
	}

	return expressions, nil
}

func (lts *SolanaLogTriggerService) getFinalizedBlockNumber(ctx context.Context, triggerID string) (int64, error) {
	block, err := lts.SolanaService.GetSlotHeight(ctx, solana.GetSlotHeightRequest{
		Commitment: solana.CommitmentFinalized,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get latest finalized block: '%w' for triggerID: %s", err, triggerID)
	}
	lts.lggr.Debugf("Latest finalized block number: %d, triggerID: %s", block.Height, triggerID)
	return int64(block.Height), nil
}

type SolanaLogTriggerService struct {
	services.Service
	srvcEng *services.Engine

	SolanaService     types.SolanaService
	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder

	triggers                        SolanaLogTriggerStore
	logTriggerPollInterval          time.Duration
	logTriggerSendChannelBufferSize uint64

	retention               time.Duration
	maxLogsKept             int64
	eventRateLimit          limits.RateLimiter
	eventPayloadSizeLimiter limits.BoundLimiter[commoncfg.Size]
}

type LogTriggerServiceOpts struct {
	SolanaService                   types.SolanaService
	Logger                          logger.Logger
	BeholderProcessor               beholder.ProtoProcessor
	MessageBuilder                  *monitoring.MessageBuilder
	Triggers                        SolanaLogTriggerStore
	LogTriggerPollInterval          time.Duration
	LogTriggerSendChannelBufferSize uint64
	Retention                       time.Duration
	MaxLogsKept                     int64
	LimitsFactory                   limits.Factory
}

func NewLogTriggerService(opts LogTriggerServiceOpts) (*SolanaLogTriggerService, error) {
	if opts.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	// Set defaults if not provided
	if opts.LogTriggerPollInterval == 0 {
		opts.LogTriggerPollInterval = 1 * time.Second
	}
	if opts.LogTriggerSendChannelBufferSize == 0 {
		opts.LogTriggerSendChannelBufferSize = 1000
	}
	if opts.Retention == 0 {
		opts.Retention = 24 * time.Hour
	}
	if opts.MaxLogsKept == 0 {
		opts.MaxLogsKept = 10000
	}

	if opts.BeholderProcessor == nil {
		return nil, fmt.Errorf("beholderProcessor is required")
	}
	if opts.MessageBuilder == nil {
		return nil, fmt.Errorf("messageBuilder is required")
	}
	lts := &SolanaLogTriggerService{
		SolanaService:                   opts.SolanaService,
		lggr:                            opts.Logger,
		beholderProcessor:               opts.BeholderProcessor,
		messageBuilder:                  opts.MessageBuilder,
		triggers:                        opts.Triggers,
		logTriggerPollInterval:          opts.LogTriggerPollInterval,
		logTriggerSendChannelBufferSize: opts.LogTriggerSendChannelBufferSize,
		retention:                       opts.Retention,
		maxLogsKept:                     opts.MaxLogsKept,
	}

	if err := lts.initLimiters(opts.LimitsFactory); err != nil {
		return nil, err
	}

	// Initialize the service engine
	lts.Service, lts.srvcEng = services.Config{
		Name:  "SolanaLogTriggerService",
		Start: lts.start,
	}.NewServiceEngine(opts.Logger)

	lts.lggr.Info("SolanaLogTriggerService initialized")

	return lts, nil
}

func (lts *SolanaLogTriggerService) initLimiters(limitsFactory limits.Factory) error {
	var err error
	lts.eventRateLimit, err = limitsFactory.MakeRateLimiter(
		cresettings.Default.PerWorkflow.LogTrigger.EventRateLimit)
	if err != nil {
		return fmt.Errorf("failed to create event rate limiter: %w", err)
	}
	lts.eventPayloadSizeLimiter, err = limits.MakeBoundLimiter(limitsFactory,
		cresettings.Default.PerWorkflow.LogTrigger.EventSizeLimit)
	if err != nil {
		return fmt.Errorf("failed to create event payload size limiter: %w", err)
	}
	return nil
}

func (lts *SolanaLogTriggerService) start(_ context.Context) error {
	duration := 30 * time.Second
	ticker := services.NewTicker(duration)
	lts.lggr.Debugf("Starting clean up of stale log poller filters every %s", duration)
	lts.srvcEng.GoTick(ticker, lts.cleanUpStaleFilters)
	return nil
}

func (lts *SolanaLogTriggerService) cleanUpStaleFilters(ctx context.Context) {
	lts.lggr.Debugf("Starting cleanUpStaleFilters")
	telemetryContext := monitoring.TelemetryContext{
		TsStart: time.Now().UnixMilli(),
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID: "solana-log-trigger-cleanup",
		},
	}

	filterNames, err := lts.SolanaService.GetFiltersNames(ctx)
	if err != nil {
		summary := fmt.Sprintf("failed to get filter names: %v", err)
		monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor,
			lts.messageBuilder.BuildLogTriggerCleanUpError(telemetryContext, summary, err.Error()))
		return
	}

	toCleanUp := make(map[string]struct{})
	for _, filterName := range filterNames {
		if strings.HasSuffix(filterName, SuffixLogTriggerFilterID) {
			toCleanUp[filterName] = struct{}{}
		}
	}

	triggers := lts.triggers.ReadAll()
	for triggerID := range triggers {
		filterID := lts.generateFilterID(triggerID)
		delete(toCleanUp, filterID)
	}

	lts.lggr.Debugf("Found %d filters to clean up", len(toCleanUp))

	for filterID := range toCleanUp {
		if err := lts.SolanaService.UnregisterLogTracking(ctx, filterID); err != nil {
			summary := fmt.Sprintf("failed to unregister stale filter %s: %v", filterID, err)
			monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor,
				lts.messageBuilder.BuildLogTriggerCleanUpError(telemetryContext, summary, err.Error()))
		}
	}
}

func (lts *SolanaLogTriggerService) RegisterLogTrigger(ctx context.Context, triggerID string, meta capabilities.RequestMetadata, config *solanacappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*solanacappb.Log], caperrors.Error) {
	lts.lggr.Debugf("RegisterLogTrigger called with triggerID: %s, config: %+v", triggerID, config)
	ctx = meta.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	if triggerID == "" {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("no triggerID provided"), caperrors.Internal)
	}

	if err := validateFilterConfig(config); err != nil {
		return nil, newUserError(err)
	}

	if _, exists := lts.triggers.Read(triggerID); exists {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("triggerID %q is already registered", triggerID), caperrors.Internal)
	}

	// Get the current finalized block number as the starting point
	fromBlock, err := lts.getFinalizedBlockNumber(ctx, triggerID)
	if err != nil {
		return nil, caperrors.NewPrivateSystemError(err, caperrors.Unavailable)
	}

	lpFilter, err := lts.ToLogPollerFilter(triggerID, config)
	if err != nil {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to create log poller filter: %w", err), caperrors.Internal)
	}

	lts.lggr.Debugf("RegisterLogTracking id: %s", lpFilter.Name)

	// Diagnostic: log what we're sending (before gRPC / ToProto)
	hasCPI := lpFilter.CPIFilterConfig != nil
	lts.lggr.Infow("[DEBUG] RegisterLogTracking sending",
		"filterName", lpFilter.Name,
		"hasCPIFilterConfig", hasCPI,
	)
	if hasCPI {
		lts.lggr.Infow("[DEBUG] RegisterLogTracking sending CPI config",
			"filterName", lpFilter.Name,
			"methodName", lpFilter.CPIFilterConfig.MethodName,
		)
	}

	err = lts.SolanaService.RegisterLogTracking(ctx, *lpFilter)
	if err != nil {
		summary := fmt.Sprintf("failed to register log-tracking: '%v' for triggerID: %s", err, triggerID)
		lts.logAndEmitError(ctx, telemetryContext, triggerID, summary, err.Error())
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s", err, triggerID), caperrors.Unknown)
	}

	lts.emitInitiated(ctx, telemetryContext, config)

	logCh := make(chan capabilities.TriggerAndId[*solanacappb.Log], lts.logTriggerSendChannelBufferSize)

	lts.srvcEng.Go(func(svcCtx context.Context) {
		pollCtx, cancel := context.WithCancel(svcCtx)
		pollCtx = meta.ContextWithCRE(pollCtx)

		pollWG := new(sync.WaitGroup)
		pollWG.Add(1)
		lts.triggers.Write(triggerID, solanaLogTriggerState{
			stopPolling: func() {
				cancel()
				pollWG.Wait()
			},
			filter: config,
		})

		defer pollWG.Done()
		lts.startPolling(pollCtx, telemetryContext, config, triggerID, fromBlock, logCh)
		lts.triggers.Delete(triggerID)
	})

	return logCh, nil
}

func (lts *SolanaLogTriggerService) UnregisterLogTrigger(ctx context.Context, triggerID string, meta capabilities.RequestMetadata, _ *solanacappb.FilterLogTriggerRequest) caperrors.Error {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	if triggerID == "" {
		return caperrors.NewPublicSystemError(fmt.Errorf("no triggerID provided"), caperrors.Internal)
	}

	trigger, found := lts.triggers.Read(triggerID)
	if !found {
		return caperrors.NewPublicSystemError(fmt.Errorf("no active trigger found for triggerID: %s", triggerID), caperrors.Internal)
	}

	lts.lggr.Debugf("UnregisterLogTrigger triggerID: %s", triggerID)
	trigger.stopPolling()
	lts.triggers.Delete(triggerID)

	err := lts.SolanaService.UnregisterLogTracking(ctx, lts.generateFilterID(triggerID))
	if err != nil {
		summary := fmt.Sprintf("failed to unregister log-tracking: '%v' for triggerID: %s", err, triggerID)
		lts.logAndEmitError(ctx, telemetryContext, triggerID, summary, err.Error())
		unregisterLogTrackingError := fmt.Errorf("failed to unregister log-tracking: '%w' for triggerID: %s", err, triggerID)
		return caperrors.NewPrivateSystemError(unregisterLogTrackingError, caperrors.Unknown)
	}
	return nil
}

// generateLogIdentifier creates the trigger event id, a unique identifier for the log based on its transaction signature and log index
func (lts *SolanaLogTriggerService) generateLogIdentifier(log *solana.Log) string {
	return fmt.Sprintf("%x:%d", log.TxHash, log.LogIndex)
}

func (lts *SolanaLogTriggerService) generateFilterID(triggerID string) string {
	return triggerID + SuffixLogTriggerFilterID
}

// checkLimitsOnLog checks the rate limit and payload size limit for a single log event, it should not error as we
// want to continue processing other logs even if one fails the limits
func (lts *SolanaLogTriggerService) checkLimitsOnLog(ctx context.Context, telemetryContext monitoring.TelemetryContext, protoLog *solanacappb.Log, triggerID string, eventID string, log *solana.Log) bool {
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

func (lts *SolanaLogTriggerService) startPolling(ctx context.Context, telemetryContext monitoring.TelemetryContext, config *solanacappb.FilterLogTriggerRequest, triggerID string, startingBlock int64, logCh chan capabilities.TriggerAndId[*solanacappb.Log]) {
	lts.lggr.Debugf("Starting polling for triggerID: %s, interval: %d", triggerID, lts.logTriggerPollInterval)
	ticker := time.NewTicker(lts.logTriggerPollInterval)
	defer ticker.Stop()
	defer close(logCh)

	// Use the finalized block number from registration as the starting point
	lastProcessedBlock := startingBlock

	for {
		select {
		case <-ctx.Done():
			lts.lggr.Debugf("Context cancelled for triggerID: %s, stopping polling", triggerID)
			return
		case <-ticker.C:
			lts.lggr.Debugf("Awake, polling for triggerID: %s, currentOffset: %d", triggerID, lastProcessedBlock)
			expressions, err := BuildQueryExpressions(config, lastProcessedBlock)
			if err != nil {
				summary := fmt.Sprintf("Failed to build query expressions for trigger %s: %v", triggerID, err)
				lts.logAndEmitError(ctx, telemetryContext, triggerID, summary, err.Error())
				continue
			}

			limitAndSort := query.NewLimitAndSort(
				query.CountLimit(defaultQueryLimit),
				query.NewSortBySequence(query.Asc),
			)

			logs, err := lts.SolanaService.QueryTrackedLogs(ctx, expressions, limitAndSort)
			if err != nil {
				summary := fmt.Sprintf("Failed to query tracked logs for trigger %s: %v", triggerID, err)
				lts.logAndEmitError(ctx, telemetryContext, triggerID, summary, err.Error())
				continue
			}

			sentCount := 0
			calculatedLatestBlock := lastProcessedBlock
			for _, log := range logs {
				if log == nil {
					lts.lggr.Warnw("Received nil log from QueryTrackedLogs, skipping", "triggerID", triggerID)
					continue
				}

				// Track the highest block number from logs (all logs from log poller are already finalized)
				if log.BlockNumber > calculatedLatestBlock {
					calculatedLatestBlock = log.BlockNumber
				}

				protoLog := solanacappb.ConvertLogToProto(log)
				eventID := lts.generateLogIdentifier(log)

				checksLimitsOk := lts.checkLimitsOnLog(ctx, telemetryContext, protoLog, triggerID, eventID, log)
				if !checksLimitsOk {
					continue
				}

				response := capabilities.TriggerAndId[*solanacappb.Log]{
					Id:      eventID,
					Trigger: protoLog,
				}

				select {
				case <-ctx.Done():
					return
				case logCh <- response:
					sentCount++
				default:
					summary := fmt.Sprintf("Callback channel full (buffer size: %d), dropping event (triggerID: %s, eventID: %s)", lts.logTriggerSendChannelBufferSize, triggerID, eventID)
					lts.logAndEmitEventDroppedError(ctx, telemetryContext, triggerID, log, summary, "channel full", false)
				}
			}

			lastProcessedBlock = calculatedLatestBlock
			successMessage := fmt.Sprintf("Finished updating BlockNumber for triggerID: %s, BlockNumber: %d, sent logs: %d", triggerID, calculatedLatestBlock, sentCount)
			lts.logAndEmitSuccess(ctx, successMessage, telemetryContext, triggerID, config, sentCount, calculatedLatestBlock)
		}
	}
}

func getEventSig(eventName string) solana.EventSignature {
	sig := lptypes.NewEventSignatureFromName(eventName)
	return solana.EventSignature(sig[:])
}

func getSubkeyPaths(subkeys []*solanacappb.SubkeyConfig) [][]string {
	paths := make([][]string, len(subkeys))
	for i, subkey := range subkeys {
		if subkey != nil {
			paths[i] = subkey.Path
		}
	}
	return paths
}

// Monitoring helper methods

func (lts *SolanaLogTriggerService) emitInitiated(ctx context.Context, tc monitoring.TelemetryContext, config *solanacappb.FilterLogTriggerRequest) {
	monitoring.EmitInitiated(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerInitiated(tc, config))
}

func (lts *SolanaLogTriggerService) logAndEmitSuccess(ctx context.Context, successMessage string, tc monitoring.TelemetryContext, triggerID string, config *solanacappb.FilterLogTriggerRequest, logCount int, latestOffsetBlock int64) {
	monitoring.LogAndEmitSuccess(ctx, successMessage, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerSuccess(tc, triggerID, config, logCount, latestOffsetBlock))
}

func (lts *SolanaLogTriggerService) logAndEmitError(ctx context.Context, tc monitoring.TelemetryContext, triggerID string, summary, cause string) {
	monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerError(tc, triggerID, summary, cause))
}

func (lts *SolanaLogTriggerService) logAndEmitEventDroppedError(ctx context.Context, tc monitoring.TelemetryContext, triggerID string, log *solana.Log, summary, cause string, isLimitError bool) {
	monitoring.LogAndEmitError(ctx, lts.lggr, lts.beholderProcessor, lts.messageBuilder.BuildLogTriggerEventDroppedError(tc, triggerID, log, summary, cause, isLimitError))
}
