package main

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

func (c *capability) RegisterTrigger(_ context.Context, _ capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (c *capability) UnregisterTrigger(_ context.Context, _ capabilities.TriggerRegistrationRequest) error {
	//TODO implement me
	panic("implement me")
}
const suffixLogTriggerFilterID = "-log-trigger"

// func (c *capabilityGRPCService) RegisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmservice.Log], error) {
// 	if triggerID == "" {
// 		return nil, fmt.Errorf("no triggerID provided")
// 	}
// 	if _, exists := c.triggers[triggerID]; exists {
// 		return nil, fmt.Errorf("triggerID %q is already registered", triggerID)
// 	}
// 	if len(input.GetAddresses()) == 0 {
// 		return nil, fmt.Errorf("no valid addresses provided (at least one address is required)")
// 	}
// 	if len(input.GetEventSigs()) == 0 {
// 		return nil, fmt.Errorf("no valid event sig provided (at least one event sig is required)")
// 	}
// 	if (input.GetConfidence() == evmcappb.ConfidenceLevel_FINALIZED || input.GetConfidence() == evmcappb.ConfidenceLevel_EARLY) && input.GetBlockDepth() != 0 {
// 		return nil, fmt.Errorf("block depth must be zero when confidence is FINALIZED or EARLY, but got: %d", input.GetBlockDepth())
// 	}

// 	c.mutexCapabilityTriggers.RLock()
// 	defer c.mutexCapabilityTriggers.RUnlock()

// 	fromBlock, err := c.calculateFromBlock(ctx, triggerID, input)
// 	if err != nil {
// 		return nil, err
// 	}

// 	logCh := make(chan capabilities.TriggerAndId[*evmservice.Log])
// 	subCtx, cancel := context.WithCancel(ctx)
// 	c.triggers[triggerID] = &logTriggerState{
// 		cancelFunc: cancel,
// 		lastBlock:  fromBlock,
// 	}

// 	filter := evmtypes.LPFilterQuery{
// 		Name:      c.generateFilterID(triggerID),
// 		Addresses: evmservice.ConvertAddressesFromProto(input.GetAddresses()),
// 		EventSigs: evmservice.ConvertHashesFromProto(input.GetEventSigs()),
// 		Topic2:    evmservice.ConvertHashesFromProto(input.GetTopic2()),
// 		Topic3:    evmservice.ConvertHashesFromProto(input.GetTopic3()),
// 		Topic4:    evmservice.ConvertHashesFromProto(input.GetTopic4()),
// 	}
// 	err = c.EVMService.RegisterLogTracking(ctx, filter)

// 	if err != nil {
// 		return nil, fmt.Errorf("failed to register log-tracking: '%w' for triggerID: %s, addresses: %v, eventSig: %v, topic2: %v, topic3: %v, topic4: %v",
// 			err, triggerID, filter.Addresses, filter.EventSigs, filter.Topic2, filter.Topic3, filter.Topic4)
// 	}
// 	go c.startPolling(subCtx, triggerID, input, logCh)

// 	return logCh, nil
// }

// func (c *capabilityGRPCService) calculateFromBlock(ctx context.Context, triggerID string, input *evmcappb.FilterLogTriggerRequest) (*big.Int, error) {
// 	var fromBlock *big.Int
// 	latest, finalized, err := c.EVMService.LatestAndFinalizedHead(ctx)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to register latest and finalized head: '%w' for triggerID: %s", err, triggerID)
// 	}
// 	switch input.GetConfidence() {
// 	case evmcappb.ConfidenceLevel_FINALIZED:
// 		fromBlock = finalized.Number
// 	case evmcappb.ConfidenceLevel_BLOCK_DEPTH:
// 		fromBlock = new(big.Int).Sub(latest.Number, big.NewInt(int64(input.GetBlockDepth())))
// 	default: // maps to ConfidenceLevel_EARLY
// 		fromBlock = new(big.Int).Sub(latest.Number, big.NewInt(c.blockDepth))
// 	}
// 	c.lggr.Debugf("Calculating from block %d", fromBlock)
// 	return fromBlock, nil
// }

// func (c *capabilityGRPCService) generateFilterID(triggerID string) string {
// 	return triggerID + suffixLogTriggerFilterID
// }

// func (c *capabilityGRPCService) startPolling(ctx context.Context, triggerID string, input *evmcappb.FilterLogTriggerRequest, logCh chan capabilities.TriggerAndId[*evmservice.Log]) {
// 	c.lggr.Debugf("Starting polling for triggerID: %s, interval: %d", triggerID, c.logTriggerPollInterval)
// 	ticker := defaultTickerFactory.NewTicker(c.logTriggerPollInterval)
// 	defer ticker.Stop()
// 	defer close(logCh)

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			c.lggr.Debugf("Stopping polling for triggerID: %s", triggerID)
// 			return
// 		case <-ticker.Channel():
// 			c.lggr.Debugf("Awake, polling for triggerID: %s", triggerID)

// 			c.mutexCapabilityTriggers.RLock()
// 			state, exists := c.triggers[triggerID]
// 			c.mutexCapabilityTriggers.RUnlock()
// 			if !exists {
// 				c.lggr.Debugf("Unregistered while polling triggerID: %s", triggerID)
// 				return
// 			}
// 			currentBlock := state.lastBlock

// 			logs, err := c.fetchLogsFromLogPoller(ctx, input, currentBlock)
// 			if err != nil {
// 				c.lggr.Errorf("Failed to fetch logs for triggerID: %s, error: %v", triggerID, err)
// 				//TODO PLEX-1457: should we sent an error to some o11y place?
// 				continue
// 			}

// 			c.lggr.Debugf("Got %d logs, sending it to the workflow trigger ID: %s, logs: %v", len(logs), triggerID, logs)
// 			for _, log := range logs {
// 				logCh <- capabilities.TriggerAndId[*evmservice.Log]{
// 					Id:      c.generateLogIdentifier(log),
// 					Trigger: log,
// 				}
// 			}
// 			c.lggr.Debugf("Finished sending events for triggerID: %s, about to update latest block number (current BlockNumber:BlockNumber %d)", triggerID, state.lastBlock)
// 			state.lastBlock = c.getLatestBlockNumber(logs, state.lastBlock)
// 			c.lggr.Debugf("Finished updating BlockNumber for triggerID: %s, BlockNumber: %d", triggerID, state.lastBlock)
// 		}
// 	}
// }

// func (c *capabilityGRPCService) generateLogIdentifier(log *evmservice.Log) string {
// 	return fmt.Sprintf("%s:%s:%d", log.GetTxHash(), log.GetBlockHash(), log.GetIndex())
// }

// func (c *capabilityGRPCService) getLatestBlockNumber(logs []*evmservice.Log, currentBlockNumber *big.Int) *big.Int {
// 	for _, l := range logs {
// 		// it has to iterate over all logs to update the last block number, as it could be multiple addresses with different block numbers among them
// 		blockNumber := new(big.Int).SetBytes(l.BlockNumber.AbsVal)
// 		if blockNumber.Cmp(currentBlockNumber) > 0 {
// 			currentBlockNumber = blockNumber
// 		}
// 	}
// 	return currentBlockNumber
// }

// func (c *capabilityGRPCService) fetchLogsFromLogPoller(ctx context.Context, input *evmcappb.FilterLogTriggerRequest, fromBlock *big.Int) ([]*evmservice.Log, error) {
// 	expressions, limitAndSort, confidence, err := c.createLogRequest(ctx, input, fromBlock)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to create log request: %v", err)
// 	}
// 	logs, err := c.EVMService.QueryTrackedLogs(ctx, expressions, limitAndSort, confidence)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to fetch logs: %w", err)
// 	}
// 	return evmservice.ConvertLogsToProto(logs), nil
// }

// func (c *capabilityGRPCService) createLogRequest(ctx context.Context, input *evmcappb.FilterLogTriggerRequest, fromBlock *big.Int) ([]query.Expression, query.LimitAndSort, primitives.ConfidenceLevel, error) {
// 	var expressions []query.Expression

// 	var addressFilters []query.Expression
// 	for _, addr := range input.GetAddresses() {
// 		addressFilters = append(addressFilters, evm.NewAddressFilter(evmtypes.Address(addr)))
// 	}
// 	expressions = append(expressions, query.Or(addressFilters...))

// 	var topicFilters []query.Expression
// 	for _, topic := range input.GetEventSigs() {
// 		topicFilters = append(topicFilters, evm.NewEventSigFilter(evmtypes.Hash(topic)))
// 	}
// 	expressions = append(expressions, query.Or(topicFilters...))

// 	var confidenceLevel primitives.ConfidenceLevel
// 	switch input.GetConfidence() {
// 	case evmcappb.ConfidenceLevel_FINALIZED:
// 		confidenceLevel = primitives.Finalized
// 	default:
// 		//Default here for either ConfidenceLevel_FINALIZED or ConfidenceLevel_EARLY
// 		confidenceLevel = primitives.Unconfirmed
// 	}

// 	if expr := c.makeEventByTopicFilter(1, input.GetTopic2()); expr != nil {
// 		expressions = append(expressions, *expr)
// 	}
// 	if expr := c.makeEventByTopicFilter(2, input.GetTopic3()); expr != nil {
// 		expressions = append(expressions, *expr)
// 	}
// 	if expr := c.makeEventByTopicFilter(3, input.GetTopic4()); expr != nil {
// 		expressions = append(expressions, *expr)
// 	}
// 	block := fmt.Sprintf("%d", fromBlock)
// 	expressions = append(expressions, query.Block(block, primitives.Gt))

// 	toBlockExpression, err := c.getToBlockExpression(ctx, input)
// 	if err != nil {
// 		return nil, query.LimitAndSort{}, primitives.Unconfirmed, err
// 	}
// 	if toBlockExpression != nil {
// 		expressions = append(expressions, *toBlockExpression)
// 	}

// 	limitAndSort := query.LimitAndSort{
// 		SortBy: []query.SortBy{
// 			query.NewSortByBlock(query.Asc),
// 		},
// 		Limit: query.Limit{},
// 	}
// 	return expressions, limitAndSort, confidenceLevel, nil
// }

// func (c *capabilityGRPCService) makeEventByTopicFilter(topic uint64, topics [][]byte) *query.Expression {
// 	if len(topics) == 0 {
// 		return nil
// 	}
// 	values := make([]evmtypes.Hash, 0, len(topics))
// 	for _, topic := range topics {
// 		values = append(values, evmtypes.Hash(topic))
// 	}
// 	expr := evm.NewEventByTopicFilter(topic, []evm.HashedValueComparator{{
// 		Values:   values,
// 		Operator: primitives.Eq,
// 	}})
// 	return &expr
// }

// func (c *capabilityGRPCService) getToBlockExpression(ctx context.Context, input *evmcappb.FilterLogTriggerRequest) (*query.Expression, error) {
// 	if input.GetConfidence() == evmcappb.ConfidenceLevel_FINALIZED {
// 		return nil, nil
// 	}
// 	// If the confidence is EARLY, we need to add a block depth filter
// 	latest, finalized, err := c.EVMService.LatestAndFinalizedHead(ctx)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to register latest and finalized head: '%w'", err)
// 	}
// 	var blockDepth int64
// 	if input.GetConfidence() == evmcappb.ConfidenceLevel_EARLY {
// 		blockDepth = c.blockDepth
// 	} else {
// 		blockDepth = int64(input.GetBlockDepth())

// 	}
// 	fromBlock := new(big.Int).Sub(latest.Number, big.NewInt(blockDepth))
// 	if fromBlock.Cmp(finalized.Number) < 0 {
// 		// If the calculated fromBlock is less than the finalized block, it means the block depth is too high, and we will default to the finalized block
// 		fromBlock = finalized.Number
// 	}
// 	var toBlockExpression query.Expression
// 	toBlockExpression = query.Block(fmt.Sprintf("%d", fromBlock), primitives.Lte)
// 	return &toBlockExpression, nil
// }

// func (c *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, _ capabilities.RequestMetadata, _ *evmcappb.FilterLogTriggerRequest) error {
// 	if triggerID == "" {
// 		return fmt.Errorf("no triggerID provided")
// 	}
// 	c.mutexCapabilityTriggers.Lock()
// 	defer c.mutexCapabilityTriggers.Unlock()

// 	existing, found := c.triggers[triggerID]
// 	if !found {
// 		return fmt.Errorf("no active trigger found for triggerID: %s", triggerID)
// 	}
// 	c.lggr.Debugf("Unregistering triggerID: %s", triggerID)
// 	existing.cancelFunc()
// 	delete(c.triggers, triggerID)
// 	err := c.EVMService.UnregisterLogTracking(ctx, c.generateFilterID(triggerID))
// 	if err != nil {
// 		//TODO PLEX-1456: once the clean up is implemented decide if we want to return an error here or just log it
// 		return fmt.Errorf("failed to unregister log-tracking: '%w' for triggerID: %s", err, triggerID)
// 	}
// 	return nil
// }

// // tickerFactory allows to mock time.Ticker in tests.
// type tickerFactory interface {
// 	NewTicker(time.Duration) tickerWrapper
// }

// type tickerWrapper interface {
// 	Channel() <-chan time.Time
// 	Stop()
// }
// type realTickerFactory struct{}

// func (realTickerFactory) NewTicker(d time.Duration) tickerWrapper {
// 	return realTicker{time.NewTicker(d)}
// }

// type realTicker struct {
// 	t *time.Ticker
// }

// func (r realTicker) Channel() <-chan time.Time {
// 	return r.t.C
// }

// func (r realTicker) Stop() {
// 	r.t.Stop()
// }

// var defaultTickerFactory tickerFactory = realTickerFactory{}
