package chainconsensus

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

func ReadHashableRequestReport[T proto.Message](ctx context.Context, handler RequestHandler, request types.HashableRequest[T]) (*capabilities.ResponseAndMetadata[T], error) {
	report, err := ReadType[*types.HashableRequestReport](ctx, handler, request)
	if err != nil {
		return nil, err
	}

	observation, ok := request.GetObservationByReportData(report.ReportData)
	if !ok {
		return nil, capabilities.ErrResponsePayloadNotAvailable
	}

	sigs := make([]capabilities.AttributedSignature, len(report.AttributedOnchainSignature))
	for i, sig := range report.AttributedOnchainSignature {
		sigs[i] = capabilities.AttributedSignature{
			Signature: sig.Signature,
			Signer:    uint32(sig.Signer),
		}
	}
	return &capabilities.ResponseAndMetadata[T]{
		Response:         observation,
		ResponseMetadata: request.GetMetadata(),
		OCRAttestation: &capabilities.OCRAttestation{
			ConfigDigest:   report.ConfigDigest,
			SequenceNumber: report.SeqNr,
			Sigs:           sigs,
		},
	}, nil
}

func ReadType[T any](ctx context.Context, reader RequestHandler, request types.Request) (T, error) {
	var zero T
	resultCh, err := reader.Handle(ctx, request)
	if err != nil {
		return zero, err
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case reply := <-resultCh:
		if reply.Err != nil {
			return zero, reply.Err
		}
		data, ok := reply.Value.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected result type: expected %T, got %T", zero, reply.Value)
		}

		return data, nil
	}
}

func ReadDecimal(ctx context.Context, handler RequestHandler, request types.Request) (decimal.Decimal, error) {
	rawDecimal, err := ReadType[*valuespb.Decimal](ctx, handler, request)
	if err != nil {
		return decimal.Decimal{}, err
	}

	return decimal.NewFromBigInt(valuespb.NewIntFromBigInt(rawDecimal.Coefficient), rawDecimal.Exponent), nil
}
