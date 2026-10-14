package gasmeter

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"
)

type recordingEmitter struct{ records []*meteringpb.MeterRecord }

func (r *recordingEmitter) Emit(_ context.Context, body []byte, _ ...any) error {
	var rec meteringpb.MeterRecord
	if err := proto.Unmarshal(body, &rec); err != nil {
		return err
	}
	r.records = append(r.records, &rec)
	return nil
}

func enabledCfg(emitter resourcemanager.Emitter) resourcemanager.Config {
	return resourcemanager.Config{
		MeterRecordsEnabled:   true,
		MeterSnapshotsEnabled: true, // must be forced off for usage records
		Emitter:               emitter,
		DeploymentIdentity:    resourcemanager.DeploymentIdentity{Product: "cre", Tenant: "t", Environment: "test", Zone: "z", NodeID: "node-1"},
	}
}

func TestNew(t *testing.T) {
	t.Run("nil when MeterRecordsEnabled is false", func(t *testing.T) {
		require.Nil(t, New(logger.Test(t), resourcemanager.Config{Emitter: &recordingEmitter{}}, 1, 7))
		var m *Meter
		require.NoError(t, m.Start(t.Context()))
		require.NoError(t, m.Close())
		m.Emit(t.Context(), capabilities.RequestMetadata{}, "0x", big.NewInt(1)) // no panic
	})

	t.Run("logs an error when enabled without an emitter", func(t *testing.T) {
		lggr, obs := logger.TestObserved(t, zapcore.ErrorLevel)
		cfg := enabledCfg(nil)
		require.NotNil(t, New(lggr, cfg, 1, 7))
		require.Len(t, obs.FilterMessage("Capability usage metering enabled but this LOOP has no durable emitter; gas usage records will not be delivered").All(), 1)
	})
}

func TestEmit(t *testing.T) {
	metadata := capabilities.RequestMetadata{WorkflowID: "wf-1", WorkflowExecutionID: "exec-1", OrgID: "org-1"}
	fee, _ := new(big.Int).SetString("123456789012345678901234", 10)

	t.Run("emits one gas record keyed by tx hash and logs the contract line", func(t *testing.T) {
		lggr, obs := logger.TestObserved(t, zapcore.InfoLevel)
		emitter := &recordingEmitter{}
		m := New(lggr, enabledCfg(emitter), 421614, 7)
		require.NotNil(t, m)

		m.Emit(t.Context(), metadata, "0xabc", fee)

		require.Len(t, emitter.records, 1)
		rec := emitter.records[0]
		require.Equal(t, meteringpb.MeterAction_METER_ACTION_USAGE, rec.GetAction())
		id := rec.GetIdentity()
		require.Equal(t, resourcemanager.EmittingServiceChainWrite, id.GetService())
		require.Equal(t, "cre:workflow:gas", id.GetResourcePool())
		require.Equal(t, "cre:workflow:gas:421614", id.GetResourcePoolId())
		require.Equal(t, "7", id.GetDon().GetDonId())
		require.Equal(t, "node-1", id.GetDon().GetNodeId())
		require.Len(t, rec.GetUtilizations(), 1)
		u := rec.GetUtilizations()[0]
		require.Equal(t, "cre:workflow:gas:421614", u.GetResourceType())
		require.Equal(t, "wf-1:exec-1", u.GetResourceId())
		require.Equal(t, "0xabc", u.GetEventId())
		require.Equal(t, "org-1", u.GetOrgId())
		require.Equal(t, "123456789012345678901234", u.GetValue())

		logs := obs.FilterMessage("Emitted capability usage meter record").All()
		require.Len(t, logs, 1)
		fields := logs[0].ContextMap()
		require.Equal(t, "exec-1", fields["executionID"])
		require.Equal(t, "0xabc", fields["eventID"])
		require.Equal(t, "cre:workflow:gas:421614", fields["resourceType"])
		require.Equal(t, "123456789012345678901234", fields["value"])
		require.Equal(t, "org-1", fields["orgID"])
		require.Equal(t, "0xabc", fields["txHash"])
	})

	t.Run("unknown DON id leaves the identity DON unset", func(t *testing.T) {
		emitter := &recordingEmitter{}
		m := New(logger.Test(t), enabledCfg(emitter), 1, 0)
		m.Emit(t.Context(), metadata, "0xabc", fee)
		require.Len(t, emitter.records, 1)
		require.Empty(t, emitter.records[0].GetIdentity().GetDon().GetDonId())
	})

	t.Run("no-op without a fee", func(t *testing.T) {
		emitter := &recordingEmitter{}
		m := New(logger.Test(t), enabledCfg(emitter), 1, 7)
		m.Emit(t.Context(), metadata, "0xabc", nil)
		require.Empty(t, emitter.records)
	})

	t.Run("refuses a malformed resource id instead of emitting a bad record", func(t *testing.T) {
		lggr, obs := logger.TestObserved(t, zapcore.ErrorLevel)
		emitter := &recordingEmitter{}
		m := New(lggr, enabledCfg(emitter), 1, 7)
		m.Emit(t.Context(), capabilities.RequestMetadata{WorkflowID: "", WorkflowExecutionID: "exec-1"}, "0xabc", fee)
		require.Empty(t, emitter.records)
		require.Len(t, obs.FilterMessage("Gas usage meter record not emitted").All(), 1)
	})
}
