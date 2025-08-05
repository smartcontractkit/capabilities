package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	action "github.com/smartcontractkit/capabilities/confidential_http_action/action"
	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
)

const (
	serviceName = "ConfidentialHTTPCapability"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type capabilitiesServer struct {
	action             capabilities.ExecutableCapability
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: logger.Sugared(lggr)}
}

func main() {
	loopserver.Serve(serviceName, New)
}
func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := cs.action.Info(ctx)
	if err != nil {
		return err
	}
	err = cs.capabilityRegistry.Remove(ctx, info.ID)
	if err != nil {
		return err
	}
	return nil
}

func (cs *capabilitiesServer) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *capabilitiesServer) Name() string {
	return serviceName
}

func (cs *capabilitiesServer) Ready() error {
	return nil
}

func (cs *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	triggerInfo, err := cs.action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		triggerInfo,
	}, nil
}

func (cs *capabilitiesServer) Initialise(
	ctx context.Context,
	config string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	relayerSet core.RelayerSet,
	oracleFactory core.OracleFactory,
	_ core.GatewayConnector,
	keystore core.Keystore) error {

	cs.lggr.Infof("Initialising %s", serviceName)
	cs.lggr.Infof("Config: %s", config)

	var capConfig cap.Config
	err := json.Unmarshal([]byte(config), &capConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if len(capConfig.VaultDONID) == 0 {
		return fmt.Errorf("VaultDONID must be provided in capability config to retrieve VaultDON capability")
	}

	vaultDONCapability, err := capabilityRegistry.GetExecutable(ctx, vault.CapabilityID)
	if err != nil {
		return fmt.Errorf("failed to get VaultDON capability with ID '%s' from registry: %w", vault.CapabilityID, err)
	}

	// vaultDONIDStr := string(capConfig.VaultDONID)
	// vaultDONIDUint, err := strconv.ParseUint(vaultDONIDStr, 10, 32)
	// if err != nil {
	// 	return fmt.Errorf("failed to parse VaultDONID '%s' as uint32: %w", vaultDONIDStr, err)
	// }

	// TODO: this has to be fetched after the capability is initialized, due to a race condition in CRE.
	vaultDONMasterPublicKey := []byte{}

	vaultDonThreshold := 3

	vaultDONPossibleFaultyNodes := 1

	cs.action, err = action.New(cs.lggr, capConfig, keystore, vaultDONCapability, vaultDONMasterPublicKey, vaultDonThreshold, vaultDONPossibleFaultyNodes)
	if err != nil {
		return fmt.Errorf("failed to create confidential http action: %w", err)
	}

	if err := capabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add attested http capability to the capability registry: %w", err)
	}
	return nil
}

func getVaultDONPossibleFaultyNodes(ctx context.Context, vaultDONCapability capabilities.ExecutableCapability) (int, error) {
	capabilityInfo, err := vaultDONCapability.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get VaultDON capability info: %w", err)
	}
	return int(capabilityInfo.DON.F), nil
}

// The generic function to get a value from the configuration.
// It takes the key and the expected type as a string for error messages.
func getValueFromConfig[T any](config capabilities.CapabilityConfiguration, key string) (T, error) {
	var zero T // A zero-value of type T to return on error

	if config.DefaultConfig == nil {
		return zero, fmt.Errorf("config.DefaultConfig is nil, cannot retrieve '%s'", key)
	}

	val, ok := config.DefaultConfig.Underlying[key]
	if !ok {
		return zero, fmt.Errorf("'%s' key not found in DefaultConfig", key)
	}

	// Unwrap the Value interface
	unwrappedValue, err := val.Unwrap()
	if err != nil {
		return zero, fmt.Errorf("error unwrapping '%s': %w", key, err)
	}

	// Type assertion to the generic type T
	finalValue, ok := unwrappedValue.(T)
	if !ok {
		return zero, fmt.Errorf("'%s' unwrapped to unexpected type: %T, expected %T", key, unwrappedValue, zero)
	}

	return finalValue, nil
}

func getVaultDONMasterPublicKey(vaultDONCapConfig capabilities.CapabilityConfiguration) ([]byte, error) {
	return getValueFromConfig[[]byte](vaultDONCapConfig, "masterPublicKey")
}

func getThreshold(vaultDONCapConfig capabilities.CapabilityConfiguration) (int, error) {
	return getValueFromConfig[int](vaultDONCapConfig, "threshold")
}
