package consensus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"

	"google.golang.org/protobuf/proto"
)

type contractReader interface {
	GetLatestValueWithHeadData(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) (*types.Head, error)
}

type consensusHandler interface {
	StartConsensusRequest(ctx context.Context, requestID string, observationsBeforeHeightReset int) (<-chan []byte, error)
	StopConsensusRequest(ctx context.Context, requestID string)
	AddObservationForRequest(ctx context.Context, requestID string, height uint64, value []byte) error
}

type Response struct {
	Value *values.Value
	Err   error
}

type ContractReader struct {
	contractReader                contractReader
	consensusHandler              consensusHandler
	readIdentifier                string
	clock                         clockwork.Clock
	pollInterval                  time.Duration
	observationsBeforeHeightReset int
}

func NewContractReader(contractReader contractReader, consensusHandler consensusHandler,
	readIdentifier string,
	clock clockwork.Clock,
	pollInterval time.Duration,
	observationsBeforeHeightReset int) *ContractReader {
	return &ContractReader{
		contractReader:                contractReader,
		consensusHandler:              consensusHandler,
		readIdentifier:                readIdentifier,
		clock:                         clock,
		pollInterval:                  pollInterval,
		observationsBeforeHeightReset: observationsBeforeHeightReset,
	}
}

func (c *ContractReader) GetLatestValue(ctx context.Context, requestID string,
	confidenceLevel primitives.ConfidenceLevel, params any) (<-chan Response, error) {
	respCh := make(chan Response, 1)

	consensusRespCh, err := c.consensusHandler.StartConsensusRequest(ctx, requestID, c.observationsBeforeHeightReset)
	if err != nil {
		return nil, fmt.Errorf("failed to add start consensus request: %w", err)
	}

	ticker := c.clock.NewTicker(c.pollInterval)
	go func() {
		defer ticker.Stop()
		defer c.consensusHandler.StopConsensusRequest(ctx, requestID)
		defer close(respCh)
		for {
			select {
			case <-ctx.Done():
				respCh <- Response{Err: errors.New("context done")}
				return
			case <-ticker.Chan():
				var value values.Value
				headData, err := c.contractReader.GetLatestValueWithHeadData(ctx, c.readIdentifier, confidenceLevel, params, &value)
				if err != nil {
					respCh <- Response{Err: fmt.Errorf("failed to get latest value fron contract reader: %w", err)}
					return
				}

				// No head data available, wait for the next poll
				if headData == nil {
					continue
				}

				valueBytes, height, err := c.getBytesAndHeight(value, *headData)
				if err != nil {
					respCh <- Response{Err: fmt.Errorf("failed to get value bytes: %w", err)}
					return
				}

				if err = c.consensusHandler.AddObservationForRequest(ctx, requestID, height, valueBytes); err != nil {
					// Ignore duplicate observation errors, as the reader is polling they are expected
					if !errors.Is(err, requests.ErrObservationForHeightAlreadyExists) {
						respCh <- Response{Err: fmt.Errorf("failed to add observation: %w", err)}
						return
					}
				}
			case resp := <-consensusRespCh:
				value, err := getValueFromBytes(resp)
				if err != nil {
					respCh <- Response{Err: fmt.Errorf("failed to get value from bytes: %w", err)}
					return
				}

				respCh <- Response{Value: &value}
				return
			}
		}
	}()

	return respCh, nil
}

func getValueFromBytes(resp []byte) (values.Value, error) {
	valuepb := pb.Value{}
	if err := proto.Unmarshal(resp, &valuepb); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	value, err := values.FromProto(&valuepb)
	if err != nil {
		return nil, fmt.Errorf("failed to convert value: %w", err)
	}
	return value, nil
}

func (c *ContractReader) getBytesAndHeight(value values.Value, headData types.Head) ([]byte, uint64, error) {
	valuepb := values.Proto(value)
	valueBytes, err := proto.Marshal(valuepb)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal value: %w", err)
	}

	height, err := strconv.ParseUint(headData.Height, 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse height: %w", err)
	}

	return valueBytes, height, nil
}
