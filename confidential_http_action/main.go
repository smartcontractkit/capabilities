package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	action "github.com/smartcontractkit/capabilities/confidential_http_action/action"
	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
)

const (
	serviceName = "ConfidentialHTTPCapability"
)

type confidentialhttpaction interface {
	capabilities.ExecutableCapability
	Start(context.Context) error
	Close() error
}

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

	// TODO: change VaultDONID to VaultCapabilityID
	vaultDONIDStr := string(capConfig.VaultDONID)
	vaultDONCapability, err := capabilityRegistry.GetExecutable(ctx, "vault@1.0.0")
	if err != nil {
		return fmt.Errorf("failed to get VaultDON capability with ID '%s' from registry: %w", vaultDONIDStr, err)
	}

	// vaultDONIDUint, err := strconv.ParseUint(vaultDONIDStr, 10, 32)
	// if err != nil {
	// 	return fmt.Errorf("failed to parse VaultDONID '%s' as uint32: %w", vaultDONIDStr, err)
	// }

	// TODO: this has to be fetched after the capability is initialized, due to a race condition in CRE.
	vaultDONMasterPublicKey := []byte{}

	cs.action, err = action.New(cs.lggr, capConfig, keystore, vaultDONCapability, vaultDONMasterPublicKey)
	if err != nil {
		return fmt.Errorf("failed to create confidential http action: %w", err)
	}

	if err := capabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add attested http capability to the capability registry: %w", err)
	}
	return nil
}

func getVaultDONMasterPublicKey(vaultDONCapConfig capabilities.CapabilityConfiguration) ([]byte, error) {
	var VaultDONMasterPublicKey []byte
	if vaultDONCapConfig.DefaultConfig != nil {
		// Access the Underlying map
		if val, ok := vaultDONCapConfig.DefaultConfig.Underlying["masterPublicKey"]; ok {
			// Unwrap the Value interface to its concrete type (string)
			pk, err := val.Unwrap() // Unwrap returns any, error
			if err != nil {
				return nil, fmt.Errorf("Error unwrapping 'masterPublicKey': %w", err)
			} else if finalPKBytes, ok := pk.([]byte); ok {
				VaultDONMasterPublicKey = finalPKBytes
				fmt.Printf("Successfully retrieved VaultDONMasterPublicKey: %s\n", VaultDONMasterPublicKey)
			} else {
				return nil, fmt.Errorf("'masterPublicKey' unwrapped to unexpected type: %T\n", pk)
			}
		} else {
			return nil, fmt.Errorf("'masterPublicKey' key not found in DefaultConfig.")
		}
	} else {
		return nil, fmt.Errorf("VaultDONCapConfig.DefaultConfig is nil, cannot retrieve 'masterPublicKey'.")
	}
	return VaultDONMasterPublicKey, nil
}
