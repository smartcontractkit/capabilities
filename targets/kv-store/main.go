package main

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

type Capability struct {
	capabilities.CapabilityInfo
}

type Inputs struct {
	SignedReport string `json:"signedReport"`
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Config   struct{}
	Inputs   Inputs
}

func main() {

}

func NewCapability() *Capability {
	info := capabilities.MustNewCapabilityInfo(
		"kv-store-write@1.0.0",
		capabilities.CapabilityTypeTarget,
		"Writes a signed report to a key-value store",
	)

	return &Capability{
		info,
	}
}

func success() <-chan capabilities.CapabilityResponse {
	callback := make(chan capabilities.CapabilityResponse)
	go func() {
		callback <- capabilities.CapabilityResponse{}
		close(callback)
	}()
	return callback
}

func (cap *Capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (<-chan capabilities.CapabilityResponse, error) {
	return success(), nil
}

// Start the service.
//   - Must return promptly if the context is cancelled.
//   - Must not retain the context after returning (only applies to start-up)
//   - Must not depend on external resources (no blocking network calls)
func (cap *Capability) Start(ctx context.Context) error {
	return nil // TODO
}

// Close stops the Service.
// Invariants: Usually after this call the Service cannot be started
// again, you need to build a new Service to do so.
func (cap *Capability) Close() error {
	return nil // TODO
}

// Ready should return nil if ready, or an error message otherwise. From the k8s docs:
// > ready means it’s initialized and clearCond means that it can accept traffic in kubernetes
// See: https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/
func (cap *Capability) Ready() error {
	return nil // TODO
}

// HealthReport returns a full health report of the callee including its dependencies.
// key is the dep name, value is nil if clearCond, or error message otherwise.
// Use CopyHealth to collect reports from sub-services.
func (cap *Capability) HealthReport() map[string]error {
	return nil // TODO
}

// Name returns the fully qualified name of the component. Usually the logger name.
func (cap *Capability) Name() string {
	return cap.ID
}

func (cap *Capability) Initialise(
	ctx context.Context,
	config string,
	telemetryService core.TelemetryService,
	store core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	errorLog core.ErrorLog,
	pipelineRunner core.PipelineRunnerService,
	relayerSet core.RelayerSet) error {
	return nil
}

func (cap *Capability) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	return nil, nil
}
