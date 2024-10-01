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

	configTracker, err := oracle.NewContractConfigTracker(cs.s.Logger, oracleIdentity)
	if err != nil {
		return fmt.Errorf("error when creating a contract confit tracker: %w", err)
	}

	oracle, err := oracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: ocrtypes.LocalConfig{
			BlockchainTimeout:                  time.Second * 10,
			ContractConfigTrackerPollInterval:  time.Second * 10,
			ContractConfigConfirmations:        1,
			ContractTransmitterTransmitTimeout: time.Second * 10,
			DatabaseTimeout:                    time.Second * 10,
		},
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(cs.s.Logger),
		ContractTransmitter:           oracle.NewContractTransmitter(cs.s.Logger),
		ContractConfigTracker:         configTracker,
		OffchainConfigDigester:        oracle.NewOffchainConfigDigester(cs.s.Logger),
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}

	err = oracle.Start(ctx)
	if err != nil {
		return fmt.Errorf("error when starting oracle: %w", err)
	}
	cs.oracle = oracle

	return nil
}
