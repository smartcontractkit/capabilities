package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/capabilities/kvstore/oracle"
	"github.com/smartcontractkit/capabilities/kvstore/target"
)

type capabilitiesServer struct {
	requestsStore *kvrequests.RequestsStore
	Target        capabilities.TargetCapability
	oracle        core.Oracle
	s             *loop.Server
	name          string
}

func New(s *loop.Server, serviceName string) *capabilitiesServer {
	return &capabilitiesServer{
		name: serviceName,
		s:    s,
	}
}

func (c *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (c *capabilitiesServer) Close() error {
	// TODO: Close missing context
	return c.oracle.Close(context.Background())
}

func (c *capabilitiesServer) Ready() error {
	return nil
}

func (c *capabilitiesServer) HealthReport() map[string]error {
	return nil
}

func (c *capabilitiesServer) Name() string {
	return c.name
}

func (c *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	targetInfo, err := c.Target.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		targetInfo,
	}, nil
}

func (c *capabilitiesServer) Initialise(
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
	c.s.Logger.Debugf("Initialising %s", c.name)

	requestsStore, err := kvrequests.New(store)
	if err != nil {
		return fmt.Errorf("error when creating requests store: %w", err)
	}

	c.requestsStore = requestsStore
	c.Target = target.New(target.Params{
		RequestsStore: c.requestsStore,
		Logger:        c.s.Logger,
	})

	c.s.Logger.Debug("config: ", config)

	var oracleIdentity oracle.Identity
	if err := json.Unmarshal([]byte(config), &oracleIdentity); err != nil {
		return fmt.Errorf("failed to unmarshal key bundle bytes: %w", err)
	}
	c.s.Logger.Debug("oracleIdentity: ", oracleIdentity)

	if err := capabilityRegistry.Add(ctx, c.Target); err != nil {
		return fmt.Errorf("error when adding kv store target to the registry: %w", err)
	}

	contractConfigTracker, err := oracle.NewContractConfigTracker(c.s.Logger, oracleIdentity)
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
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(c.s.Logger, c.requestsStore),
		ContractTransmitter:           oracle.NewContractTransmitter(c.s.Logger, oracleIdentity),
		ContractConfigTracker:         contractConfigTracker,                        // UNUSED
		OffchainConfigDigester:        oracle.NewOffchainConfigDigester(c.s.Logger), // UNUSED
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}
	c.s.Logger.Debug("KVStore capabilities: Oracle created")

	err = oracle.Start(ctx)
	if err != nil {
		return fmt.Errorf("error when starting oracle: %w", err)
	}
	c.s.Logger.Debug("KVStore capabilities: Oracle started")
	c.oracle = oracle

	return nil
}
