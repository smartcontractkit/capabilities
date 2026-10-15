package capability

// embedConfig is the embed command's own config, registered on that command alone.
type embedConfig struct {
	// Instances is how many instances of the capability this process hosts.
	Instances int `validate:"min=1"`
}

// defaultEmbeddedDonID is the DON an embedded run reports. An embed run is one DON in one process,
// so there is only ever the one, and requiring it to be configured would mean typing the same
// number into every embedded run.
const defaultEmbeddedDonID = 1

// embeddedCapabilityConfig is capabilityConfig as an embed run registers it, replacing run's under
// the same namespace. It keeps the DON ID and drops the proxy URL: there is no node behind an
// embedded run to dial, so only what this binary hosts resolves.
type embeddedCapabilityConfig struct {
	// CapabilityDonID is the on-chain DON ID reported for the capabilities this process hosts.
	CapabilityDonID uint32
}
