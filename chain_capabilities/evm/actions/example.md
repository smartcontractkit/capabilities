Convert the following EVM BalanceAt method to be a Solana BalanceAt method

```golang
func (e *EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.BalanceAtRequest) (*capabilities.ResponseAndMetadata[*evm.BalanceAtReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.BalanceAt)); err != nil {
		return nil, NewUserError(err)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, NewUserError(err)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtInitiated(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64()))

	balanceAt := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, NewUserError(fmt.Errorf("error getting call block number: %w", err))
		}

		address, err := evmservice.ConvertOptionalAddressFromProto(req.GetAccount())
		if err != nil {
			return nil, NewUserError(fmt.Errorf("error converting address from proto: %w", err))
		}

		reply, err := e.EVMService.BalanceAt(ctx, evmtypes.BalanceAtRequest{
			Address:         address,
			BlockNumber:     callBlockNumber,
			ConfidenceLevel: confidenceLevel,
		})
		if err != nil {
			return nil, err
		}

		pbBalance := valuespb.NewBigIntFromInt(reply.Balance)
		return proto.Marshal(pbBalance)
	}

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), balanceAt)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return balanceAt(ctx, nil)
		})
	}

	balance := new(valuespb.BigInt)
	if err := e.readProto(ctx, request, balance); err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildBalanceAtError(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), "Failed to read BalanceAt", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read BalanceAt", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildBalanceAtSuccess(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), valuespb.NewIntFromBigInt(balance)))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.BalanceAtReply]{
		Response:         &evm.BalanceAtReply{Balance: balance},
		ResponseMetadata: metering.GetResponseMetadata(metering.BalanceAt),
	}
	return &responseAndMetadata, nil
}
```

we want to fill out the Solana
```
func (s *Solana) GetBalance(
    ctx context.Context,
    metadata capabilities.RequestMetadata,
    input *solcap.GetBalanceRequest) (*capabilities.ResponseAndMetadata[*solcap.GetBalanceReply], caperrors.Error) {
    // TODO
    return nil, GetError(errors.New("unimplemented"), false)
}
```