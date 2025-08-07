package server

import (
	"context"
	"fmt"
	"time"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/kvstore/action"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/capabilities/kvstore/oracle"
	"github.com/smartcontractkit/capabilities/kvstore/target"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type capabilitiesServer struct {
	Action             capabilities.ExecutableCapability
	Target             capabilities.ExecutableCapability
	oracle             core.Oracle
	lggr               logger.SugaredLogger
	capabilityRegistry core.CapabilitiesRegistry
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: logger.Sugared(lggr)}
}

func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() error {
	err := cs.oracle.Close(context.Background())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = cs.capabilityRegistry.Remove(ctx, action.ID)
	if err != nil {
		return err
	}

	err = cs.capabilityRegistry.Remove(ctx, target.ID)
	if err != nil {
		return err
	}

	return nil
}

func (cs *capabilitiesServer) Ready() error {
	return nil
}

func (cs *capabilitiesServer) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *capabilitiesServer) Name() string {
	return cs.lggr.Name()
}

func (cs *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	targetInfo, err := cs.Target.Info(ctx)
	if err != nil {
		return nil, err
	}

	actionInfo, err := cs.Action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		actionInfo,
		targetInfo,
	}, nil
}

func (cs *capabilitiesServer) Initialise(
	ctx context.Context,
	_ string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	oracleFactory core.OracleFactory,
	_ core.GatewayConnector,
	_ core.Keystore,
) error {
	cs.lggr.Debug("Initialising")

	requestsStore, err := kvrequests.New(cs.lggr)
	if err != nil {
		return fmt.Errorf("error when creating requests store: %w", err)
	}

	cs.Action = action.New(action.Params{
		Logger:        cs.lggr,
		RequestsStore: requestsStore,
	})
	cs.Target = target.New(target.Params{
		Logger:        cs.lggr,
		RequestsStore: requestsStore,
	})

	cs.capabilityRegistry = capabilityRegistry

	if err := capabilityRegistry.Add(ctx, cs.Action); err != nil {
		return fmt.Errorf("error when adding kv store action to the registry: %w", err)
	}
	if err := capabilityRegistry.Add(ctx, cs.Target); err != nil {
		return fmt.Errorf("error when adding kv store target to the registry: %w", err)
	}

	oracle, err := oracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: ocrtypes.LocalConfig{
			BlockchainTimeout:                  time.Second * 20,
			ContractConfigTrackerPollInterval:  time.Second * 10,
			ContractConfigConfirmations:        1,
			ContractTransmitterTransmitTimeout: time.Second * 10,
			DatabaseTimeout:                    time.Second * 10,
		},
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(cs.lggr, requestsStore),
		ContractTransmitter:           oracle.NewContractTransmitter(cs.lggr, requestsStore),
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}
	cs.lggr.Debug("KVStore capabilities: Oracle created")

	err = oracle.Start(ctx)
	if err != nil {
		return fmt.Errorf("error when starting oracle: %w", err)
	}
	cs.lggr.Debug("KVStore capabilities: Oracle started")
	cs.oracle = oracle

	return nil
}
