package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/go-plugin"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/capabilities/kvstore/oracle"
	"github.com/smartcontractkit/capabilities/kvstore/target"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	serviceName = "KVStoreCapabilities"
)

type CapabilitiesService struct {
	requestsStore *kvrequests.RequestsStore
	target        capabilities.TargetCapability
	oracle        core.Oracle
	s             *loop.Server
}

func main() {
	s := loop.MustNewStartedServer(serviceName)
	defer s.Stop()

	s.Logger.Infof("Starting service %s", serviceName)

	stopCh := make(chan struct{})
	defer close(stopCh)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: loop.StandardCapabilitiesHandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			loop.PluginStandardCapabilitiesName: &loop.StandardCapabilitiesLoop{
				PluginServer: &CapabilitiesService{
					s: s,
				},
				BrokerConfig: loop.BrokerConfig{Logger: s.Logger, StopCh: stopCh, GRPCOpts: s.GRPCOpts},
			},
		},
		GRPCServer: s.GRPCOpts.NewServer,
	})
}

func (cs *CapabilitiesService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapabilitiesService) Close() error {
	// TODO: Close missing context
	return cs.oracle.Close(context.Background())
}

func (cs *CapabilitiesService) Ready() error {
	return nil
}

func (cs *CapabilitiesService) HealthReport() map[string]error {
	return nil
}

func (cs *CapabilitiesService) Name() string {
	return serviceName
}

func (cs *CapabilitiesService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	targetInfo, err := cs.target.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		targetInfo,
	}, nil
}

type JSONConfig map[string]interface{}

// Bytes returns the raw bytes
func (r JSONConfig) Bytes() []byte {
	b, _ := json.Marshal(r)
	return b
}

func (cs *CapabilitiesService) Initialise(
	ctx context.Context,
	config string,
	telemetryService core.TelemetryService,
	store core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	errorLog core.ErrorLog,
	pipelineRunner core.PipelineRunnerService,
	relayerSet core.RelayerSet,
	oracleFactory core.OracleFactory,
) error {
	cs.s.Logger.Debugf("Initialising %s", serviceName)

	cs.requestsStore = kvrequests.New(store)
	cs.target = target.New(target.Params{
		RequestsStore: cs.requestsStore,
		Logger:        cs.s.Logger,
	})

	cs.s.Logger.Debug("config: ", config)

	// var keyBundle ocr2key.KeyBundle
	// store.Get(ctx, "key_bundle", &keyBundle)

	var oracleIdentity oracle.Identity
	if err := json.Unmarshal([]byte(config), &oracleIdentity); err != nil {
		return fmt.Errorf("failed to unmarshal key bundle bytes: %w", err)
	}
	cs.s.Logger.Debug("oracleIdentity: ", oracleIdentity)

	if err := capabilityRegistry.Add(ctx, cs.target); err != nil {
		return fmt.Errorf("error when adding kv store target to the registry: %w", err)
	}

	// relayer, err := relayerSet.Get(ctx, types.RelayID{Network: "evm", ChainID: "31337"})
	// if err != nil {
	// 	return fmt.Errorf("error when getting relayer: %w", err)
	// }

	// type RelayConfig struct {
	// 	ChainID                string   `json:"chainID"`
	// 	EffectiveTransmitterID string   `json:"effectiveTransmitterID"`
	// 	SendingKeys            []string `json:"sendingKeys"`
	// }

	// // pluginName = "ocr-capability"
	// // providerType = "ocr3-capability"
	// var relayConfig = RelayConfig{
	// 	ChainID:                "31337",
	// 	EffectiveTransmitterID: oracleIdentity.EVMKey,
	// 	SendingKeys:            []string{oracleIdentity.EVMKey},
	// }
	// relayConfigBytes, err := json.Marshal(relayConfig)
	// if err != nil {
	// 	return fmt.Errorf("error when marshalling relay config: %w", err)
	// }

	// type PipelineSpec struct {
	// 	Name string `json:"name"`
	// 	Spec string `json:"spec"`
	// }

	// type Config struct {
	// 	Pipelines    []PipelineSpec `json:"pipelines"`
	// 	PluginConfig map[string]any `json:"pluginConfig"`
	// }

	// type innerConfig struct {
	// 	Command       string            `json:"command"`
	// 	EnvVars       map[string]string `json:"envVars"`
	// 	ProviderType  string            `json:"providerType"`
	// 	PluginName    string            `json:"pluginName"`
	// 	TelemetryType string            `json:"telemetryType"`
	// 	OCRVersion    int               `json:"OCRVersion"`
	// 	Config
	// }

	// pluginProvider, err := relayer.NewPluginProvider(ctx, core.RelayArgs{
	// 	ContractID:   "0x2279B7A0a67DB372996a5FaB50D91eAA73d2eBe6",
	// 	ProviderType: "plugin",
	// 	RelayConfig:  relayConfigBytes,
	// }, core.PluginArgs{
	// 	TransmitterID: oracleIdentity.EVMKey,
	// 	PluginConfig: JSONConfig{
	// 		"pluginName": "kvstore-capability",
	// 		"OCRVersion": 3,
	// 	}.Bytes(),
	// })
	// if err != nil {
	// 	return fmt.Errorf("error when getting offchain digester: %w", err)
	// }

	// newContext := context.Background()

	contractConfigTracker, err := oracle.NewContractConfigTracker(cs.s.Logger, oracleIdentity)
	if err != nil {
		return fmt.Errorf("error when creating contract config tracker: %w", err)
	}

	oracle, err := oracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: ocrtypes.LocalConfig{
			BlockchainTimeout:                  time.Second * 20,
			ContractConfigTrackerPollInterval:  time.Second * 10,
			ContractConfigConfirmations:        1,
			ContractTransmitterTransmitTimeout: time.Second * 10,
			DatabaseTimeout:                    time.Second * 10,
		},
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(cs.s.Logger),
		ContractTransmitter:           oracle.NewContractTransmitter(cs.s.Logger, oracleIdentity),
		ContractConfigTracker:         contractConfigTracker,
		OffchainConfigDigester:        oracle.NewOffchainConfigDigester(cs.s.Logger),
		// ContractConfigTracker:         pluginProvider.ContractConfigTracker(),
		// OffchainConfigDigester: pluginProvider.OffchainConfigDigester(),
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}
	cs.s.Logger.Debug("KVStore capabilities: Oracle created")

	err = oracle.Start(ctx)
	if err != nil {
		return fmt.Errorf("error when starting oracle: %w", err)
	}
	cs.s.Logger.Debug("KVStore capabilities: Oracle started")
	cs.oracle = oracle

	return nil
}
