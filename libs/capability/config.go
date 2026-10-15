package capability

//go:generate go run ./internal/docgen docs

import "github.com/samber/do/v2"

const capabilitiesNamespace = "capabilities"

const grpcNamespace = "grpc"

const settingsNamespace = "settings"

const defaultHost = "localhost"

// grpcConfig is where the gRPC listener the registry serves this process's capabilities on binds.
type grpcConfig struct {
	// AdvertiseHost is the host the gRPC servers this process opens bind to and are advertised at.
	// Empty binds every interface.
	AdvertiseHost string
}

// registerSettings registers the run's own settings, as any dependency registers its config, for
// RunErr to bind and start to resolve. capabilities differs between the commands: embed's has no
// proxy to dial and defaults the DON, and capabilityConfig is built from it.
func registerSettings() {
	ProvideConfig(telemetryNamespace, TelemetryConfig{})
	ProvideConfig(tracingNamespace, TracingConfig{SamplingRatio: 1})
	ProvideConfig(chipIngressNamespace, ChipIngressConfig{})
	ProvideConfig(pyroscopeNamespace, PyroscopeConfig{})
	ProvideConfig(httpNamespace, HTTPConfig{Port: defaultHTTPPort})
	ProvideConfig(grpcNamespace, grpcConfig{AdvertiseHost: defaultHost})
	ProvideConfig(settingsNamespace, settingsConfig{})

	ProvideConfigForRun(capabilitiesNamespace, capabilityConfig{})

	ProvideConfigForEmbed("", embedConfig{Instances: 1})
	ProvideConfigForEmbed(capabilitiesNamespace, embeddedCapabilityConfig{CapabilityDonID: defaultEmbeddedDonID})
	ProvideForEmbed(func(i do.Injector) (capabilityConfig, error) {
		cfg, err := do.Invoke[embeddedCapabilityConfig](i)
		return capabilityConfig{CapabilityDonID: cfg.CapabilityDonID}, err
	})
}
