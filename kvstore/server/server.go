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

	"github.com/smartcontractkit/capabilities/kvstore/action"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/capabilities/kvstore/oracle"
	"github.com/smartcontractkit/capabilities/kvstore/target"
)

type capabilitiesServer struct {
	Action capabilities.ActionCapability
	Target capabilities.TargetCapability
	oracle core.Oracle
	s      *loop.Server
	name   string
}

func New(s *loop.Server, serviceName string) *capabilitiesServer {
	return &capabilitiesServer{
		name: serviceName,
		s:    s,
	}
}

func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() error {
	// TODO: Close missing context
	return cs.oracle.Close(context.Background())
}

func (cs *capabilitiesServer) Ready() error {
	return nil
}

func (cs *capabilitiesServer) HealthReport() map[string]error {
	return nil
}

func (cs *capabilitiesServer) Name() string {
	return cs.name
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
	config string,
	telemetryService core.TelemetryService,
	store core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	errorLog core.ErrorLog,
	pipelineRunner core.PipelineRunnerService,
	relayerSet core.RelayerSet,
	oracleFactory core.OracleFactory,
) error {
	cs.s.Logger.Debugf("Initialising %s", cs.name)

	requestsStore, err := kvrequests.New(store, cs.s.Logger)
	if err != nil {
		return fmt.Errorf("error when creating requests store: %w", err)
	}

	cs.Action = action.New(action.Params{
		Logger:        cs.s.Logger,
		RequestsStore: requestsStore,
	})
	cs.Target = target.New(target.Params{
		Logger:        cs.s.Logger,
		RequestsStore: requestsStore,
	})

	cs.s.Logger.Debug("config: ", config)

	var oracleIdentity oracle.Identity
	if err := json.Unmarshal([]byte(config), &oracleIdentity); err != nil {
		return fmt.Errorf("failed to unmarshal key bundle bytes: %w", err)
	}
	cs.s.Logger.Debug("oracleIdentity: ", oracleIdentity)

	if err := capabilityRegistry.Add(ctx, cs.Target); err != nil {
		return fmt.Errorf("error when adding kv store target to the registry: %w", err)
	}

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
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(cs.s.Logger, requestsStore),
		ContractTransmitter:           oracle.NewContractTransmitter(cs.s.Logger, oracleIdentity, requestsStore),
		ContractConfigTracker:         contractConfigTracker,                         // UNUSED
		OffchainConfigDigester:        oracle.NewOffchainConfigDigester(cs.s.Logger), // UNUSED
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

	// =================================================================================================
	// FOR TESTING PURPOSES ONLY
	request := kvrequests.Request{
		WorkflowExecutionID: "workflowExecutionID",
		ReferenceID:         "1",
		Type:                kvrequests.RequestKindWrite,
		KVPairs: map[string][]byte{
			"key":  []byte("value"),
			"key2": []byte("value2"),
		},
	}

	err = requestsStore.Add(ctx, &request)
	if err != nil {
		return err
	}
	// =================================================================================================

	return nil
}
