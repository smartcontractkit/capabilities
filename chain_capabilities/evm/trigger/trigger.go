package trigger

import (
	"context"
	"fmt"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/evm"
	"math/big"
	"time"
)

const (
	suffixLogTriggerFilterID     = "-log-trigger"
	defaultSendChannelBufferSize = 1000
	defaultLimitQueryLogSize     = 1000
)

type LogTriggerService struct {
	EVMService             types.EVMService
	lggr                   logger.Logger
	triggers               *LogTriggerStore
	logTriggerPollInterval time.Duration
}

// NewLogTriggerService creates a new instance of logTriggerService.
func NewLogTriggerService(evmService types.EVMService, lggr logger.Logger, logTriggerPollInterval time.Duration) *LogTriggerService {
	return &LogTriggerService{
		EVMService:             evmService,
		lggr:                   lggr,
		triggers:               NewLogTriggerStore(),
		logTriggerPollInterval: logTriggerPollInterval,
	}
}

func (c LogTriggerService) Close() error {
	var errs []error
	// Unregister all log triggers
	ctx := context.Background() //TODO lautaro is this correct? I need the context for the logPoller
	for triggerID := range c.triggers.ReadAll() {
		err := c.UnregisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to unregister log trigger %s: %w", triggerID, err))
		}
	}
	//TODO PLEX-1487: make sure to cancel all working log triggers, and unregister them from the logPoller
	if len(errs) > 0 {
		return fmt.Errorf("errors occurred during Close: %v", errs)
	}
	return nil
}

func (c LogTriggerService) RegisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmservice.Log], error) {
	if triggerID == "" {
		return nil, fmt.Errorf("no triggerID provided")
	}
	if _, exists := c.triggers.Read(triggerID); exists {
		return nil, fmt.Errorf("triggerID %q is already registered", triggerID)
	}
	if len(input.GetAddresses()) == 0 {
		return nil, fmt.Errorf("no valid addresses provided (at least one address is required)")
	}
	if len(input.GetEventSigs()) == 0 {
		return nil, fmt.Errorf("no valid event sig provided (at least one event sig is required)")
	}

	fromBlock, err := c.calculateFromBlock(ctx, triggerID, input)
	if err != nil {
		return nil, err
	}

	logCh := make(chan capabilities.TriggerAndId[*evmservice.Log], defaultSendChannelBufferSize)
	subCtx, cancel := context.WithCancel(ctx)

	c.triggers.Write(triggerID, logTriggerState{
		cancelFunc: cancel,
		lastBlock:  fromBlock,
	})

	filter := evmtypes.LPFilterQuery{
		Name:      c.generateFilterID(triggerID),
		Addresses: evmservice.ConvertAddressesFromProto(input.GetAddresses()),
		EventSigs: evmservice.ConvertHashesFromProto(input.GetEventSigs()),
		Topic2:    evmservice.ConvertHashesFromProto(input.GetTopic2()),
		Topic3:    evmservice.ConvertHashesFromProto(input.GetTopic3()),
		Topic4:    evmservice.ConvertHashesFromProto(input.GetTopic4()),
	}
	err = c.EVMService.RegisterLogTracking(ctx, filter)

	if err != nil {
		return nil, fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s, addresses: %v, eventSig: %v, topic2: %v, topic3: %v, topic4: %v",
			err, triggerID, filter.Addresses, filter.EventSigs, filter.Topic2, filter.Topic3, filter.Topic4)
	}
	go c.startPolling(subCtx, triggerID, input, logCh)

	return logCh, nil
}

func (c LogTriggerService) calculateFromBlock(ctx context.Context, triggerID string, input *evmcappb.FilterLogTriggerRequest) (*big.Int, error) {
	var fromBlock *big.Int
	latest, finalized, err := c.EVMService.LatestAndFinalizedHead(ctx)
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
	c.lggr.Debugf("Calculating from block %s", fromBlock)
	return fromBlock, nil
}

func (c LogTriggerService) generateFilterID(triggerID string) string {
	return triggerID + suffixLogTriggerFilterID
}

func (c LogTriggerService) startPolling(ctx context.Context, triggerID string, input *evmcappb.FilterLogTriggerRequest, logCh chan capabilities.TriggerAndId[*evmservice.Log]) {
	c.lggr.Debugf("Starting polling for triggerID: %s, interval: %d", triggerID, c.logTriggerPollInterval)
	ticker := defaultTickerFactory.NewTicker(c.logTriggerPollInterval)
	defer ticker.Stop()
	defer close(logCh)

	for {
		select {
		case <-ctx.Done():
			c.lggr.Debugf("Stopping polling for triggerID: %s", triggerID)
			return
		case <-ticker.Channel():
			state, exists := c.triggers.Read(triggerID)
			if !exists {
				c.lggr.Debugf("Unregistered while polling triggerID: %s", triggerID)
				return
			}
			c.lggr.Debugf("Awake, polling for triggerID: %s, currentOffset: %d", triggerID, state.lastBlock)
			logs, err := c.fetchLogsFromLogPoller(ctx, input, state.lastBlock)
			if err != nil {
				c.lggr.Errorf("Failed to fetch logs for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				continue
			}
			c.lggr.Debugf("Got %d logs, sending it to the workflow trigger ID: %s", len(logs), triggerID)
			for _, log := range logs {
				logCh <- capabilities.TriggerAndId[*evmservice.Log]{
					Id:      c.generateLogIdentifier(log),
					Trigger: log,
				}
			}
			c.lggr.Debugf("Finished sending events for triggerID: %s, about to update latest block number (current BlockNumber:BlockNumber %d)", triggerID, state.lastBlock)
			calculatedLatestBlock := c.getLatestBlockNumber(logs, state.lastBlock)
			err = c.triggers.Update(triggerID, calculatedLatestBlock)
			if err != nil {
				c.lggr.Errorf("Failed to update last block for triggerID: %s, error: %v", triggerID, err)
				//TODO PLEX-1457: should we sent an error to some o11y place?
				continue
			}
			c.lggr.Debugf("Finished updating BlockNumber for triggerID: %s, BlockNumber: %d", triggerID, calculatedLatestBlock)
		}
	}
}

func (c LogTriggerService) generateLogIdentifier(log *evmservice.Log) string {
	return fmt.Sprintf("%s:%s:%d", log.GetTxHash(), log.GetBlockHash(), log.GetIndex())
}

func (c LogTriggerService) getLatestBlockNumber(logs []*evmservice.Log, currentBlockNumber *big.Int) *big.Int {
	for _, l := range logs {
		// it has to iterate over all logs to update the last block number, as it could be multiple addresses with different block numbers among them
		blockNumber := new(big.Int).SetBytes(l.BlockNumber.AbsVal)
		if blockNumber.Cmp(currentBlockNumber) > 0 {
			currentBlockNumber = blockNumber
		}
	}
	return currentBlockNumber
}

func (c LogTriggerService) fetchLogsFromLogPoller(ctx context.Context, input *evmcappb.FilterLogTriggerRequest, fromBlock *big.Int) ([]*evmservice.Log, error) {
	expressions, limitAndSort, confidence, err := c.createLogRequest(ctx, input, fromBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to create log request: %w", err)
	}
	logs, err := c.EVMService.QueryTrackedLogs(ctx, expressions, limitAndSort, confidence)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs: %w", err)
	}
	return evmservice.ConvertLogsToProto(logs), nil
}

func (c LogTriggerService) createLogRequest(ctx context.Context, input *evmcappb.FilterLogTriggerRequest, fromBlock *big.Int) ([]query.Expression, query.LimitAndSort, primitives.ConfidenceLevel, error) {
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

	if expr := c.makeEventByTopicFilter(1, input.GetTopic2()); expr != nil {
		expressions = append(expressions, *expr)
	}
	if expr := c.makeEventByTopicFilter(2, input.GetTopic3()); expr != nil {
		expressions = append(expressions, *expr)
	}
	if expr := c.makeEventByTopicFilter(3, input.GetTopic4()); expr != nil {
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

func (c LogTriggerService) makeEventByTopicFilter(topic uint64, topics [][]byte) *query.Expression {
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

func (c LogTriggerService) UnregisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, _ *evmcappb.FilterLogTriggerRequest) error {
	if triggerID == "" {
		return fmt.Errorf("no triggerID provided")
	}
	trigger, found := c.triggers.Read(triggerID)
	if !found {
		return fmt.Errorf("no active trigger found for triggerID: %s", triggerID)
	}
	c.lggr.Debugf("Unregistering triggerID: %s", triggerID)
	trigger.cancelFunc()
	c.triggers.Delete(triggerID)

	err := c.EVMService.UnregisterLogTracking(ctx, c.generateFilterID(triggerID))
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
