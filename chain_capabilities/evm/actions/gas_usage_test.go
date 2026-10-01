package actions

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

func TestEmitGasUsage(t *testing.T) {
	metadata := capabilities.RequestMetadata{WorkflowID: "wf-1", WorkflowExecutionID: "exec-1", OrgID: "org-1"}
	identity := resourcemanager.ResourceIdentity{Product: "cre", Service: resourcemanager.EmittingServiceChainWrite, ResourcePool: resourcemanager.WorkflowUsageResourcePool}
	fee, _ := new(big.Int).SetString("123456789012345678901234", 10)

	t.Run("emits one gas record keyed by tx hash and logs the contract line", func(t *testing.T) {
		lggr, obs := logger.TestObserved(t, zapcore.InfoLevel)
		emitter := &recordingEmitter{}
		rm := resourcemanager.NewResourceManager(lggr, resourcemanager.ResourceManagerConfig{MeterRecordsEnabled: true, Emitter: emitter})

		emitGasUsage(t.Context(), lggr, rm, identity, 421614, metadata, "0xabc", fee)

		require.Len(t, emitter.records, 1)
		rec := emitter.records[0]
		require.Equal(t, meteringpb.MeterAction_METER_ACTION_USAGE, rec.GetAction())
		require.Equal(t, resourcemanager.EmittingServiceChainWrite, rec.GetIdentity().GetService())
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
	})

	t.Run("no-op without a meter or fee", func(t *testing.T) {
		lggr := logger.Test(t)
		emitter := &recordingEmitter{}
		rm := resourcemanager.NewResourceManager(lggr, resourcemanager.ResourceManagerConfig{MeterRecordsEnabled: true, Emitter: emitter})
		emitGasUsage(t.Context(), lggr, nil, identity, 1, metadata, "0xabc", fee)
		emitGasUsage(t.Context(), lggr, rm, identity, 1, metadata, "0xabc", nil)
		require.Empty(t, emitter.records)
	})

	t.Run("refuses a malformed resource id instead of emitting a bad record", func(t *testing.T) {
		lggr, obs := logger.TestObserved(t, zapcore.ErrorLevel)
		emitter := &recordingEmitter{}
		rm := resourcemanager.NewResourceManager(lggr, resourcemanager.ResourceManagerConfig{MeterRecordsEnabled: true, Emitter: emitter})
		emitGasUsage(t.Context(), lggr, rm, identity, 1, capabilities.RequestMetadata{WorkflowID: "", WorkflowExecutionID: "exec-1"}, "0xabc", fee)
		require.Empty(t, emitter.records)
		require.Len(t, obs.FilterMessage("Gas usage meter record not emitted").All(), 1)
	})
}
