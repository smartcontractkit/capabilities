// Package gasmeter emits the cre:workflow:gas:<chain_selector> usage MeterRecord
// that bills one chain write. Every chain-write capability (EVM, Solana, ...)
// uses it so the record shape, identity and the reconciler log line stay
// identical across chains.
package gasmeter

import (
	"context"
	"math/big"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

// Meter emits one METER_ACTION_USAGE record per chain write. A nil *Meter is a
// no-op everywhere, so callers only need nil checks when registering it as a
// service.
type Meter struct {
	lggr         logger.Logger
	rm           *resourcemanager.ResourceManager
	identity     resourcemanager.ResourceIdentity
	resourceType string
}

// New builds a Meter from the LOOP metering config ([Metering] on the host).
// It returns nil when MeterRecordsEnabled is false: gas usage records share
// that gate with durable resource metering. Records are never snapshotted.
// capabilityDonID of 0 means unknown and leaves the DON id unset.
func New(lggr logger.Logger, cfg resourcemanager.Config, chainSelector uint64, capabilityDonID uint32) *Meter {
	if !cfg.MeterRecordsEnabled {
		return nil
	}
	rmCfg := cfg.ResourceManagerConfig
	rmCfg.MeterSnapshotsEnabled = false
	resourceType := resourcemanager.WorkflowGasResourceType(chainSelector)
	identity := resourcemanager.WithWorkflowUsagePool(
		resourcemanager.NewBaseIdentity(cfg.DeploymentIdentity, resourcemanager.EmittingServiceChainWrite, ""),
		resourceType,
	)
	if capabilityDonID != 0 {
		identity = identity.WithDonID(strconv.FormatUint(uint64(capabilityDonID), 10))
	}
	if rmCfg.Emitter == nil {
		lggr.Errorw("Capability usage metering enabled but this LOOP has no durable emitter; gas usage records will not be delivered")
	}
	return &Meter{
		lggr:         lggr,
		rm:           resourcemanager.NewResourceManager(lggr, rmCfg),
		identity:     identity,
		resourceType: resourceType,
	}
}

// Start starts the underlying ResourceManager.
func (m *Meter) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	return m.rm.Start(ctx)
}

// Close closes the underlying ResourceManager.
func (m *Meter) Close() error {
	if m == nil {
		return nil
	}
	return m.rm.Close()
}

// Emit emits the gas usage record for one chain write and logs the emission.
// txHash (or tx signature) is the capability event id: one record per
// on-chain write, identical on every node of the DON that observes it. The
// log line is a contract consumed by the billing reconciler (fields:
// executionID, eventID, resourceType, value, orgID, txHash) and must stay
// stable. Fail-open: never affects the reply.
func (m *Meter) Emit(ctx context.Context, metadata capabilities.RequestMetadata, txHash string, fee *big.Int) {
	if m == nil || fee == nil {
		return
	}
	resourceID, err := resourcemanager.WorkflowUsageResourceID(metadata.WorkflowID, metadata.WorkflowExecutionID)
	if err != nil {
		m.lggr.Errorw("Gas usage meter record not emitted", "err", err, "executionID", metadata.WorkflowExecutionID)
		return
	}
	m.rm.EmitUsageValue(ctx, m.identity, txHash, fee, resourcemanager.UtilizationFields{
		ResourceType: m.resourceType,
		ResourceID:   resourceID,
		OrgID:        metadata.OrgID,
	})
	m.lggr.Infow("Emitted capability usage meter record",
		"executionID", metadata.WorkflowExecutionID,
		"eventID", txHash,
		"resourceType", m.resourceType,
		"value", fee.String(),
		"orgID", metadata.OrgID,
		"txHash", txHash,
	)
}
