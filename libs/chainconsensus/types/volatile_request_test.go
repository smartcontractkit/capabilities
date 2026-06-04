package types

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestNewVolatileRequest(t *testing.T) {
	wf, ref := "workflow-exec-id-volatile", "ref-id-volatile"
	meta := testResponseMetadata()
	r := NewVolatileRequest(wf, ref, meta, func(context.Context) (*emptypb.Empty, uint64, error) {
		return nil, 0, nil
	}, logger.TestSugared(t))
	require.Equal(t, commonMon.RequestID(wf, ref), r.ID())
	require.Equal(t, meta, r.GetMetadata())
}

func TestVolatileRequest_GetOCRObservation(t *testing.T) {
	t.Run("no observation yet", func(t *testing.T) {
		r := NewVolatileRequest("wf", "ref", testResponseMetadata(), func(context.Context) (*emptypb.Empty, uint64, error) {
			return nil, 0, nil
		}, logger.TestSugared(t))
		ob, err := r.GetOCRObservation()
		require.Nil(t, err)
		require.Nil(t, ob)
	})

	t.Run("observation error is surfaced in VolatileObservations", func(t *testing.T) {
		r := NewVolatileRequest("wf", "ref", testResponseMetadata(), func(context.Context) (*emptypb.Empty, uint64, error) {
			return nil, 0, assert.AnError
		}, logger.TestSugared(t))
		require.Error(t, r.CaptureObservation(t.Context()))
		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		volOb, ok := ob.Observation.(*RequestObservation_Volatile)
		require.True(t, ok)
		require.Empty(t, volOb.Volatile.Observations)
		expectedErr, err := NewObservationError(assert.AnError)
		require.NoError(t, err)
		require.Equal(t, []byte(expectedErr), volOb.Volatile.Error)
	})

	t.Run("successful observation returns volatile with height and hash matching ResponseToReportData", func(t *testing.T) {
		wf, ref := "wf-volatile-success", "ref-volatile-success"
		meta := testResponseMetadata()
		payload := &wrapperspb.StringValue{Value: "deterministic-volatile-payload"}
		const wantHeight uint64 = 12345
		r := NewVolatileRequest(wf, ref, meta, func(context.Context) (*wrapperspb.StringValue, uint64, error) {
			return payload, wantHeight, nil
		}, logger.TestSugared(t))

		require.NoError(t, r.CaptureObservation(t.Context()))
		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		volOb, ok := ob.Observation.(*RequestObservation_Volatile)
		require.True(t, ok)
		require.Len(t, volOb.Volatile.Observations, 1)
		require.Empty(t, volOb.Volatile.Error)

		vo := volOb.Volatile.Observations[0]
		require.Equal(t, wantHeight, vo.Height)
		require.Len(t, vo.Hash, HashLength)

		payloadAsBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
		require.NoError(t, err)
		expectedHash, err := commoncap.ResponseToReportData(wf, ref, payloadAsBytes, meta)
		require.NoError(t, err)
		require.Equal(t, expectedHash[:], vo.Hash)

		var key Hash
		copy(key[:], vo.Hash)
		actualPayload, ok := r.GetObservationByReportData(key)
		require.True(t, ok)
		require.Empty(t, cmp.Diff(payload, actualPayload, protocmp.Transform()))
	})

	t.Run("duplicate hash keeps highest height", func(t *testing.T) {
		wf, ref := "wf-volatile-dedupe", "ref-volatile-dedupe"
		meta := testResponseMetadata()
		payload := &wrapperspb.StringValue{Value: "same-payload"}
		callsCounter := 0
		r := NewVolatileRequest(wf, ref, meta, func(context.Context) (*wrapperspb.StringValue, uint64, error) {
			if callsCounter == 0 {
				callsCounter++
				return payload, 10, nil
			}

			return payload, 5, nil
		}, logger.TestSugared(t))

		require.NoError(t, r.CaptureObservation(t.Context()))
		require.NoError(t, r.CaptureObservation(t.Context()))

		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		volOb := ob.Observation.(*RequestObservation_Volatile).Volatile
		require.Len(t, volOb.Observations, 1)
		require.Equal(t, uint64(10), volOb.Observations[0].Height)
	})

	t.Run("two distinct payloads sorted by hash", func(t *testing.T) {
		wf, ref := "wf-volatile-two", "ref-volatile-two"
		meta := testResponseMetadata()
		p1 := &wrapperspb.StringValue{Value: "a-payload"}
		p2 := &wrapperspb.StringValue{Value: "b-payload"}
		var round int
		r := NewVolatileRequest(wf, ref, meta, func(context.Context) (*wrapperspb.StringValue, uint64, error) {
			round++
			if round == 1 {
				return p2, 2, nil
			}
			return p1, 1, nil
		}, logger.TestSugared(t))
		require.NoError(t, r.CaptureObservation(t.Context()))
		require.NoError(t, r.CaptureObservation(t.Context()))

		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		volOb := ob.Observation.(*RequestObservation_Volatile).Volatile
		require.Len(t, volOb.Observations, 2)

		h1, err := hashVolatilePayload(wf, ref, meta, p1)
		require.NoError(t, err)
		h2, err := hashVolatilePayload(wf, ref, meta, p2)
		require.NoError(t, err)
		require.NotEqual(t, 0, bytes.Compare(h1[:], h2[:]))

		heightByHash := map[string]uint64{
			string(h1[:]): 1,
			string(h2[:]): 2,
		}
		for _, vo := range volOb.Observations {
			require.Equal(t, heightByHash[string(vo.Hash)], vo.Height)
		}
		requireVolatileObservationsSortedByHash(t, volOb.Observations)
	})

	t.Run("clears error on successful observation", func(t *testing.T) {
		wf, ref := "wf-volatile-mixed", "ref-volatile-mixed"
		meta := testResponseMetadata()
		payload := &wrapperspb.StringValue{Value: "mixed-payload"}
		fail := true
		r := NewVolatileRequest(wf, ref, meta, func(context.Context) (*wrapperspb.StringValue, uint64, error) {
			if fail {
				return nil, 0, assert.AnError
			}
			return payload, 7, nil
		}, logger.TestSugared(t))
		require.ErrorIs(t, r.CaptureObservation(t.Context()), assert.AnError)
		fail = false
		require.NoError(t, r.CaptureObservation(t.Context()))

		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		volOb := ob.Observation.(*RequestObservation_Volatile).Volatile
		require.Len(t, volOb.Observations, 1)
		require.Empty(t, volOb.Error)
	})

	t.Run("invalid metadata fails when building report data", func(t *testing.T) {
		r := NewVolatileRequest("wf", "ref", commoncap.ResponseMetadata{}, func(context.Context) (*emptypb.Empty, uint64, error) {
			return &emptypb.Empty{}, 1, nil
		}, logger.TestSugared(t))
		err := r.CaptureObservation(t.Context())
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to convert response to report data: failed to extract metering from metadata: unexpected number of metering records received from peer")
	})

	t.Run("max unique observations rejects additional capture", func(t *testing.T) {
		wf, ref := "wf-volatile-max", "ref-volatile-max"
		meta := testResponseMetadata()
		var n uint64
		r := NewVolatileRequest(wf, ref, meta, func(context.Context) (*wrapperspb.StringValue, uint64, error) {
			n++
			return &wrapperspb.StringValue{Value: fmt.Sprintf("payload-%d", n)}, n, nil
		}, logger.TestSugared(t))
		for i := 0; i < MaxNumberOfVolatileObservations; i++ {
			require.NoError(t, r.CaptureObservation(t.Context()), "capture %d", i)
		}
		err := r.CaptureObservation(t.Context())
		require.ErrorContains(t, err, "max number of unique observations reached")

		ob, err := r.GetOCRObservation()
		require.NoError(t, err)
		volOb := ob.Observation.(*RequestObservation_Volatile).Volatile
		require.Len(t, volOb.Observations, MaxNumberOfVolatileObservations)
		requireVolatileObservationsSortedByHash(t, volOb.Observations)
	})
}

func requireVolatileObservationsSortedByHash(t *testing.T, observations []*VolatileObservation) {
	t.Helper()
	for i := 1; i < len(observations); i++ {
		require.Less(t, bytes.Compare(observations[i-1].Hash, observations[i].Hash), 0,
			"observations must be sorted by hash in ascending byte order")
	}
}

func hashVolatilePayload(wf, ref string, meta commoncap.ResponseMetadata, payload proto.Message) (Hash, error) {
	payloadAsBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		return Hash{}, err
	}
	return commoncap.ResponseToReportData(wf, ref, payloadAsBytes, meta)
}
