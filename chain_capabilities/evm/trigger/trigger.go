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
	// limitAndSort defines the default sorting and limiting for all log queries.
	limitAndSort query.LimitAndSort
}

// NewLogTriggerService creates a new instance of logTriggerService.
// TODO PLEX-1465: the core logic of RegisterLogTrigger/UnregisterLogTrigger/Close/etc. should be moved to the EVMService, so it can be used by other services as well.
func NewLogTriggerService(evmService types.EVMService, store LogTriggerStore, lggr logger.Logger, logTriggerPollInterval time.Duration) *LogTriggerService {
	// all queries to log poller will have the same limit and sort, so we can create it once and reuse it
	limitAndSort := query.NewLimitAndSort(query.Limit{
		Count: defaultLimitQueryLogSize,
	}, query.NewSortByBlock(query.Asc))

	lts := &LogTriggerService{
		EVMService:             evmService,
		lggr:                   lggr,
		triggers:               store,
		logTriggerPollInterval: logTriggerPollInterval,
		limitAndSort:           limitAndSort,
	}

	lts.Service, lts.srvcEng = services.Config{
		Name: "EvmLogTriggerService",
	}.NewServiceEngine(lggr)

	return lts
}

func (lts *LogTriggerService) RegisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmcappb.Log], error) {
	if triggerID == "" {
		return nil, fmt.Errorf("no triggerID provided")
	}
	if _, exists := lts.triggers.Read(triggerID); exists {
		return nil, fmt.Errorf("triggerID %q is already registered", triggerID)
	}
	if len(input.GetAddresses()) == 0 {
		return nil, fmt.Errorf("no valid addresses provided (at least one address is required)")
	}
	if len(input.GetTopics()) > 4 {
		return nil, fmt.Errorf("there can be at most 4 topics provided, got %d instead", len(input.GetTopics()))
	}
	if len(input.GetTopics()) == 0 || len(input.GetTopics()[0].Values) == 0 {
		return nil, fmt.Errorf("no valid event sig provided (at least one event sig is required in topics)")
	}
	eventSigs, topics2, topics3, topics4 := lts.getTopics(input)

	fromBlock, err := lts.getFinalizedBlockNumber(ctx, triggerID)
	if err != nil {
		return nil, err
	}

	filterQuery := evmtypes.LPFilterQuery{
		Name:      lts.generateFilterID(triggerID),
		Addresses: evmservice.ConvertAddressesFromProto(input.GetAddresses()),
		EventSigs: evmservice.ConvertHashesFromProto(eventSigs),
		Topic2:    evmservice.ConvertHashesFromProto(topics2),
		Topic3:    evmservice.ConvertHashesFromProto(topics3),
		Topic4:    evmservice.ConvertHashesFromProto(topics4),
	}
	err = lts.EVMService.RegisterLogTracking(ctx, filterQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s, addresses: %v, eventSig: %v, topic2: %v, topic3: %v, topic4: %v",
			err, triggerID, filterQuery.Addresses, filterQuery.EventSigs, filterQuery.Topic2, filterQuery.Topic3, filterQuery.Topic4)
	}
	expressions, confidence := lts.createLogRequest(ctx, input.GetAddresses(), eventSigs, topics2, topics3, topics4, input.GetConfidence())

	logCh := make(chan capabilities.TriggerAndId[*evmcappb.Log], defaultSendChannelBufferSize)
	lts.srvcEng.Go(func(srvcCtx context.Context) {
		subCtx, cancel := context.WithCancel(srvcCtx)
		lts.triggers.Write(triggerID, logTriggerState{
			cancelFunc:              cancel,
			lastBlock:               fromBlock,
			unfinalizedSentEventIDs: make(map[string]*big.Int),
			filter: filter{
				expressions: expressions,
				confidence:  confidence,
			},
		})
		lts.startPolling(subCtx, triggerID, logCh)
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
	_, finalized, err := lts.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to register latest and finalized head: '%w' for triggerID: %s", err, triggerID)
	}
	lts.lggr.Debugf("Latest finalized block number: %s", finalized.Number)
	return finalized.Number, nil
}

func (lts *LogTriggerService) generateFilterID(triggerID string) string {
	return triggerID + suffixLogTriggerFilterID
}

func (lts *LogTriggerService) startPolling(ctx context.Context, triggerID string, logCh chan capabilities.TriggerAndId[*evmcappb.Log]) {
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
				lts.lggr.Errorf("Failed to fetch logs for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				continue
			}

			finalizedBlockNumber, err := lts.getFinalizedBlockNumber(ctx, triggerID)
			if err != nil {
				lts.lggr.Errorf("Failed to get latest finalized block number for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				continue
			}

			err = lts.sendLogsToWorkflows(logs, finalizedBlockNumber, triggerID, state, logCh)
			if err != nil {
				lts.lggr.Errorf("Failed to send logs for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				return
			}

			lts.lggr.Debugf("Finished sending events for triggerID: %s, about to update latest block number (current offset: %d, latest finalized block: %d)", triggerID, state.lastBlock, finalizedBlockNumber)
			calculatedLatestBlock := lts.getLatestBlockNumber(logs, state.lastBlock, finalizedBlockNumber)
			err = lts.triggers.Update(triggerID, calculatedLatestBlock, state.unfinalizedSentEventIDs)
			if err != nil {
				lts.lggr.Errorf("Failed to update last block for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				return
			}
			lts.lggr.Debugf("Finished updating BlockNumber for triggerID: %s, BlockNumber: %d", triggerID, calculatedLatestBlock)
		}
	}
}

func (lts *LogTriggerService) sendLogsToWorkflows(logs []*evmtypes.Log,
	finalizedBlockNumber *big.Int,
	triggerID string,
	trigger logTriggerState,
	logCh chan capabilities.TriggerAndId[*evmcappb.Log]) error {
	lts.lggr.Debugf("Got %d logs, sending them to the workflow trigger ID: %s", len(logs), triggerID)
	var needsUpdate bool

	for _, log := range logs {
		logID := lts.generateLogIdentifier(log)
		_, alreadySent := trigger.unfinalizedSentEventIDs[logID]
		lts.lggr.Debugf("Working with logId: %s, alreadySent: %t", logID, alreadySent)

		if !alreadySent {
			response := capabilities.TriggerAndId[*evmcappb.Log]{
				Id:      lts.generateLogIdentifier(log),
				Trigger: evmcappb.ConvertLogToProto(log),
			}
			lts.lggr.Debugf("Sending log event for triggerID: %s, block number: %d, eventID: %s", triggerID, log.BlockNumber, response.Id)

			select {
			case logCh <- response:
				if log.BlockNumber.Cmp(finalizedBlockNumber) > 0 {
					// log's block number is unfinalized and needs to be tracked
					trigger.unfinalizedSentEventIDs[logID] = log.BlockNumber
					needsUpdate = true
				}
			default:
				lts.lggr.Errorw("Callback channel full, dropping event", "triggerID", triggerID, "eventID", response.Id)
				//TODO PLEX-1457: should we sent an error to some o11y place?
			}
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
	return nil
}

// generateLogIdentifier creates the trigger event id, a unique identifier for the log based on its transaction hash, block hash, and index
func (lts *LogTriggerService) generateLogIdentifier(log *evmtypes.Log) string {
	return fmt.Sprintf("%x:%x:%d", log.TxHash, log.BlockHash, log.LogIndex)
}

func (lts *LogTriggerService) getLatestBlockNumber(logs []*evmtypes.Log, currentBlockNumber *big.Int, finalizedBlockNumber *big.Int) *big.Int {
	for _, l := range logs {
		// it has to iterate over all logs to update the last block number, as it could be multiple addresses with different block numbers among them
		blockNumber := l.BlockNumber
		if blockNumber.Cmp(currentBlockNumber) > 0 && blockNumber.Cmp(finalizedBlockNumber) <= 0 {
			currentBlockNumber = blockNumber
		}
	}
	return currentBlockNumber
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

func (lts *LogTriggerService) createLogRequest(_ context.Context, addresses, eventSigs, topics2, topics3, topics4 [][]byte, confidence evmcappb.ConfidenceLevel) ([]query.Expression, primitives.ConfidenceLevel) {
	var expressions []query.Expression

	var addressFilters []query.Expression
	for _, addr := range addresses {
		addressFilters = append(addressFilters, evm.NewAddressFilter(evmtypes.Address(addr)))
	}
	expressions = append(expressions, query.Or(addressFilters...))

	var topicFilters []query.Expression
	for _, topic := range eventSigs {
		topicFilters = append(topicFilters, evm.NewEventSigFilter(evmtypes.Hash(topic)))
	}
	expressions = append(expressions, query.Or(topicFilters...))

	var confidenceLevel primitives.ConfidenceLevel
	switch confidence {
	case evmcappb.ConfidenceLevel_FINALIZED:
		confidenceLevel = primitives.Finalized
	default:
		//TODO PLEX-1488: it has to support SAFE here.
		//Default here for either ConfidenceLevel_LATEST or ConfidenceLevel_SAFE
		confidenceLevel = primitives.Unconfirmed
	}

	if expr := lts.makeEventByTopicFilter(1, topics2); expr != nil {
		expressions = append(expressions, *expr)
	}
	if expr := lts.makeEventByTopicFilter(2, topics3); expr != nil {
		expressions = append(expressions, *expr)
	}
	if expr := lts.makeEventByTopicFilter(3, topics4); expr != nil {
		expressions = append(expressions, *expr)
	}

	//TODO PLEX-1488: when implementing SAFE we need to add a toBlockExpression to the query where it will be the latest safe block number

	return expressions, confidenceLevel
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
