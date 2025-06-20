package trigger

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
)

const (
	suffixLogTriggerFilterID     = "-log-trigger"
	defaultSendChannelBufferSize = 1000
	defaultLimitQueryLogSize     = 1000
)

type LogTriggerService struct {
	services.Service
	srvcEng *services.Engine

	EVMService             types.EVMService
	lggr                   logger.Logger
	triggers               LogTriggerStore
	logTriggerPollInterval time.Duration
}

// NewLogTriggerService creates a new instance of logTriggerService.
// TODO PLEX-1465: the core logic of RegisterLogTrigger/UnregisterLogTrigger/Close/etc. should be moved to the EVMService, so it can be used by other services as well.
func NewLogTriggerService(evmService types.EVMService, store LogTriggerStore, lggr logger.Logger, logTriggerPollInterval time.Duration) *LogTriggerService {
	lts := &LogTriggerService{
		EVMService:             evmService,
		lggr:                   lggr,
		triggers:               store,
		logTriggerPollInterval: logTriggerPollInterval,
	}

	lts.Service, lts.srvcEng = services.Config{
		Name: "EvmLogTriggerService",
	}.NewServiceEngine(lggr)

	return lts
}

func (lts *LogTriggerService) RegisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmservice.Log], error) {
	if triggerID == "" {
		return nil, fmt.Errorf("no triggerID provided")
	}
	if _, exists := lts.triggers.Read(triggerID); exists {
		return nil, fmt.Errorf("triggerID %q is already registered", triggerID)
	}
	if len(input.GetAddresses()) == 0 {
		return nil, fmt.Errorf("no valid addresses provided (at least one address is required)")
	}
	if len(input.GetEventSigs()) == 0 {
		return nil, fmt.Errorf("no valid event sig provided (at least one event sig is required)")
	}

	fromBlock, err := lts.calculateFromBlock(ctx, triggerID, input)
	if err != nil {
		return nil, err
	}

	filter := evmtypes.LPFilterQuery{
		Name:      lts.generateFilterID(triggerID),
		Addresses: evmservice.ConvertAddressesFromProto(input.GetAddresses()),
		EventSigs: evmservice.ConvertHashesFromProto(input.GetEventSigs()),
		Topic2:    evmservice.ConvertHashesFromProto(input.GetTopic2()),
		Topic3:    evmservice.ConvertHashesFromProto(input.GetTopic3()),
		Topic4:    evmservice.ConvertHashesFromProto(input.GetTopic4()),
	}
	err = lts.EVMService.RegisterLogTracking(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s, addresses: %v, eventSig: %v, topic2: %v, topic3: %v, topic4: %v",
			err, triggerID, filter.Addresses, filter.EventSigs, filter.Topic2, filter.Topic3, filter.Topic4)
	}
	logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], defaultSendChannelBufferSize)
	lts.srvcEng.Go(func(srvcCtx context.Context) {
		subCtx, cancel := context.WithCancel(srvcCtx)
		lts.triggers.Write(triggerID, logTriggerState{
			cancelFunc: cancel,
			lastBlock:  fromBlock,
		})
		lts.startPolling(subCtx, triggerID, input, logCh)
	})

	return logCh, nil
}

func (lts *LogTriggerService) calculateFromBlock(ctx context.Context, triggerID string, input *evmcappb.FilterLogTriggerRequest) (*big.Int, error) {
	var fromBlock *big.Int
	latest, finalized, err := lts.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to register latest and finalized head: '%w' for triggerID: %s", err, triggerID)
	}
	switch input.GetConfidence() {
	case evmcappb.ConfidenceLevel_FINALIZED:
		fromBlock = finalized.Number
	case evmcappb.ConfidenceLevel_LATEST:
		fromBlock = latest.Number
	default:
		//TODO PLEX-1488: it has to support SAFE here using the latest safe block number for the time being
		fromBlock = latest.Number
	}
	lts.lggr.Debugf("Calculating from block %s", fromBlock)
	return fromBlock, nil
}

func (lts *LogTriggerService) generateFilterID(triggerID string) string {
	return triggerID + suffixLogTriggerFilterID
}

func (lts *LogTriggerService) startPolling(ctx context.Context, triggerID string, input *evmcappb.FilterLogTriggerRequest, logCh chan capabilities.TriggerAndId[*evmservice.Log]) {
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
			logs, err := lts.fetchLogsFromLogPoller(ctx, input, state.lastBlock)
			if err != nil {
				lts.lggr.Errorf("Failed to fetch logs for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				continue
			}
			lts.sendLogsToWorkflows(logs, triggerID, logCh)

			lts.lggr.Debugf("Finished sending events for triggerID: %s, about to update latest block number (current BlockNumber:BlockNumber %d)", triggerID, state.lastBlock)
			calculatedLatestBlock := lts.getLatestBlockNumber(logs, state.lastBlock)
			err = lts.triggers.Update(triggerID, calculatedLatestBlock)
			if err != nil {
				lts.lggr.Errorf("Failed to update last block for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				continue
			}
			lts.lggr.Debugf("Finished updating BlockNumber for triggerID: %s, BlockNumber: %d", triggerID, calculatedLatestBlock)
		}
	}
}

func (lts *LogTriggerService) sendLogsToWorkflows(logs []*evmservice.Log, triggerID string, logCh chan capabilities.TriggerAndId[*evmservice.Log]) {
	lts.lggr.Debugf("Got %d logs, sending it to the workflow trigger ID: %s", len(logs), triggerID)
	for _, log := range logs {
		response := lts.createTriggerResponse(log)
		select {
		case logCh <- response:
		default:
			lts.lggr.Errorw("Callback channel full, dropping event", "triggerID", triggerID, "eventID", response.Id)
			//TODO PLEX-1457: should we sent an error to some o11y place?
		}
	}
}

func (lts *LogTriggerService) createTriggerResponse(log *evmservice.Log) capabilities.TriggerAndId[*evmservice.Log] {
	return capabilities.TriggerAndId[*evmservice.Log]{
		Id:      lts.generateLogIdentifier(log),
		Trigger: log,
	}
}

// generateLogIdentifier creates the trigger event id, a unique identifier for the log based on its transaction hash, block hash, and index
func (lts *LogTriggerService) generateLogIdentifier(log *evmservice.Log) string {
	return fmt.Sprintf("%s:%s:%d", log.GetTxHash(), log.GetBlockHash(), log.GetIndex())
}

func (lts *LogTriggerService) getLatestBlockNumber(logs []*evmservice.Log, currentBlockNumber *big.Int) *big.Int {
	for _, l := range logs {
		// it has to iterate over all logs to update the last block number, as it could be multiple addresses with different block numbers among them
		blockNumber := new(big.Int).SetBytes(l.BlockNumber.AbsVal)
		if blockNumber.Cmp(currentBlockNumber) > 0 {
			currentBlockNumber = blockNumber
		}
	}
	return currentBlockNumber
}

func (lts *LogTriggerService) fetchLogsFromLogPoller(ctx context.Context, input *evmcappb.FilterLogTriggerRequest, fromBlock *big.Int) ([]*evmservice.Log, error) {
	expressions, limitAndSort, confidence, err := lts.createLogRequest(ctx, input, fromBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to create log request: %w", err)
	}
	logs, err := lts.EVMService.QueryTrackedLogs(ctx, expressions, limitAndSort, confidence)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs: %w", err)
	}
	return evmservice.ConvertLogsToProto(logs), nil
}

func (lts *LogTriggerService) createLogRequest(ctx context.Context, input *evmcappb.FilterLogTriggerRequest, fromBlock *big.Int) ([]query.Expression, query.LimitAndSort, primitives.ConfidenceLevel, error) {
	var expressions []query.Expression

	var addressFilters []query.Expression
	for _, addr := range input.GetAddresses() {
		addressFilters = append(addressFilters, evm.NewAddressFilter(evmtypes.Address(addr)))
	}
	expressions = append(expressions, query.Or(addressFilters...))

	var topicFilters []query.Expression
	for _, topic := range input.GetEventSigs() {
		topicFilters = append(topicFilters, evm.NewEventSigFilter(evmtypes.Hash(topic)))
	}
	expressions = append(expressions, query.Or(topicFilters...))

	var confidenceLevel primitives.ConfidenceLevel
	switch input.GetConfidence() {
	case evmcappb.ConfidenceLevel_FINALIZED:
		confidenceLevel = primitives.Finalized
	default:
		//TODO PLEX-1488: it has to support SAFE here.
		//Default here for either ConfidenceLevel_LATEST or ConfidenceLevel_SAFE
		confidenceLevel = primitives.Unconfirmed
	}

	if expr := lts.makeEventByTopicFilter(1, input.GetTopic2()); expr != nil {
		expressions = append(expressions, *expr)
	}
	if expr := lts.makeEventByTopicFilter(2, input.GetTopic3()); expr != nil {
		expressions = append(expressions, *expr)
	}
	if expr := lts.makeEventByTopicFilter(3, input.GetTopic4()); expr != nil {
		expressions = append(expressions, *expr)
	}
	block := fmt.Sprintf("%d", fromBlock)
	expressions = append(expressions, query.Block(block, primitives.Gt))

	//TODO PLEX-1488: when implementing SAFE we need to add a toBlockExpression to the query where it will be the latest safe block number

	limitAndSort := query.LimitAndSort{
		SortBy: []query.SortBy{
			query.NewSortByBlock(query.Asc),
		},
		Limit: query.Limit{
			Count: defaultLimitQueryLogSize,
		},
	}
	return expressions, limitAndSort, confidenceLevel, nil
}

func (lts *LogTriggerService) makeEventByTopicFilter(topic uint64, topics [][]byte) *query.Expression {
	if len(topics) == 0 {
		return nil
	}
	values := make([]evmtypes.Hash, 0, len(topics))
	for _, topic := range topics {
		values = append(values, evmtypes.Hash(topic))
	}
	expr := evm.NewEventByTopicFilter(topic, []evm.HashedValueComparator{{
		Values:   values,
		Operator: primitives.Eq,
	}})
	return &expr
}

func (lts *LogTriggerService) UnregisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, _ *evmcappb.FilterLogTriggerRequest) error {
	if triggerID == "" {
		return fmt.Errorf("no triggerID provided")
	}
	trigger, found := lts.triggers.Read(triggerID)
	if !found {
		return fmt.Errorf("no active trigger found for triggerID: %s", triggerID)
	}
	lts.lggr.Debugf("Unregistering triggerID: %s", triggerID)
	trigger.cancelFunc()
	lts.triggers.Delete(triggerID)

	err := lts.EVMService.UnregisterLogTracking(ctx, lts.generateFilterID(triggerID))
	if err != nil {
		//TODO PLEX-1456: once the clean up is implemented decide if we want to return an error here or just log it
		return fmt.Errorf("failed to unregister log-tracking: '%w' for triggerID: %s", err, triggerID)
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
