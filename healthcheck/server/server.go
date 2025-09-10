package server

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var _ loop.StandardCapabilities = (*HealthCheckServer)(nil)

var (
	CronHealthCheckRegistrationCount = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "healthcheck_cron_registration_count",
			Help: "Metric representing the number of cron registrations",
		},
	)
	CronHealthCheckUnregistrationCount = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "healthcheck_cron_unregistration_count",
			Help: "Metric representing the number of cron unregistrations",
		},
	)
	CronHealthCheckTriggersCount = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "healthcheck_cron_triggers_count",
			Help: "Metric representing the number of cron triggers",
		},
	)
)

type HealthCheckServer struct {
	Lggr         logger.Logger
	capRegistry  core.CapabilitiesRegistry
	checkingCron bool
}

func (s *HealthCheckServer) Start(ctx context.Context) error {
	go s.runLoop(context.Background())
	return nil
}

func (s *HealthCheckServer) Close() error {
	return nil
}

func (s *HealthCheckServer) Ready() error {
	return nil
}

func (s *HealthCheckServer) HealthReport() map[string]error {
	return nil
}

func (s *HealthCheckServer) Name() string {
	return "HealthCheckServer"
}

func (s *HealthCheckServer) Initialise(ctx context.Context, config string, telemetryService core.TelemetryService, store core.KeyValueStore, capabilityRegistry core.CapabilitiesRegistry, errorLog core.ErrorLog, pipelineRunner core.PipelineRunnerService, relayerSet core.RelayerSet, oracleFactory core.OracleFactory, gatewayConnector core.GatewayConnector, p2pKeystore core.Keystore) error {

	s.capRegistry = capabilityRegistry
	s.Start(ctx)
	return nil
}

func (s *HealthCheckServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	return []capabilities.CapabilityInfo{
		{
			ID:             "healthcheck",
			CapabilityType: "trigger",
			Description:    "healthcheck poc",
			IsLocal:        true,
		},
	}, nil
}

func New(lggr logger.Logger) *HealthCheckServer {
	return &HealthCheckServer{
		Lggr: logger.Sugared(lggr),
	}
}

func (s *HealthCheckServer) runLoop(ctx context.Context) {
	for {
		caps, err := s.capRegistry.List(ctx)
		if err != nil {
			s.Lggr.Error(err)
			return
		}

		for _, c := range caps {
			// get cap info
			info, err2 := c.Info(ctx)
			if err2 != nil {
				s.Lggr.Error(err)
				return
			}

			switch info.ID {
			case "cron-trigger@1.0.0":
				if !s.checkingCron {
					s.checkingCron = true
					ct := CronTriggerTester{
						Lggr:        s.Lggr,
						CapRegistry: s.capRegistry,
						cap:         nil,
						state:       0,
					}
					ct.init(ctx)
					go ct.stateMachine(context.Background())
				}
				break
			}

			time.Sleep(time.Second * 30)

		}
	}
}

type CronTriggerTester struct {
	Lggr                       logger.Logger
	CapRegistry                core.CapabilitiesRegistry
	cap                        capabilities.TriggerCapability
	triggerRegistrationRequest capabilities.TriggerRegistrationRequest
	triggerCh                  <-chan capabilities.TriggerResponse
	state                      CronTriggerState
}
type CronTriggerState int

const (
	StateRegister CronTriggerState = iota
	StateIdle1
	StateIdle2
	StateUnregister
)

func (c *CronTriggerTester) init(ctx context.Context) error {
	capability, err := c.CapRegistry.GetTrigger(ctx, "cron-trigger@1.0.0")
	if err != nil {
		c.Lggr.Error(err)
		return err
	}
	c.cap = capability
	return nil
}

func (c *CronTriggerTester) createTriggerRequest() error {
	payload, err := anypb.New(&cron.Config{Schedule: "*/30 * * * * *"})
	if err != nil {
		c.Lggr.Error(err)
		return err
	}
	c.triggerRegistrationRequest = capabilities.TriggerRegistrationRequest{
		TriggerID: uuid.New().String(),
		Metadata: capabilities.RequestMetadata{
			WorkflowID: "healthcheck",
		},
		Config:  nil,
		Payload: payload,
		Method:  "",
	}
	return nil
}

func (c *CronTriggerTester) register(ctx context.Context) error {
	err := c.createTriggerRequest()
	if err != nil {
		c.Lggr.Error(err)
		return err
	}
	triggerCh, err := c.cap.RegisterTrigger(ctx, c.triggerRegistrationRequest)
	if err != nil {
		c.Lggr.Error(err)
		return err
	}
	c.triggerCh = triggerCh
	CronHealthCheckRegistrationCount.Inc()
	c.Lggr.Info("Registered cron trigger")
	go c.monitor()
	return nil
}

func (c *CronTriggerTester) unregister(ctx context.Context) error {
	err := c.cap.UnregisterTrigger(ctx, c.triggerRegistrationRequest)
	if err != nil {
		c.Lggr.Error(err)
		return err
	}
	CronHealthCheckUnregistrationCount.Inc()
	c.Lggr.Info("Unregistered cron trigger")
	return nil
}

func (c *CronTriggerTester) stateMachine(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	state := StateRegister

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			switch state {
			case StateRegister:
				if err := c.register(ctx); err != nil {
					return err
				}
				state = StateIdle1
			case StateIdle1:
				c.Lggr.Info("Idle state 1")
				state = StateIdle2
			case StateIdle2:
				c.Lggr.Info("Idle state 2")
				state = StateUnregister
			case StateUnregister:
				if err := c.unregister(ctx); err != nil {
					return err
				}
				state = StateRegister
			}
		}
	}
}

func (c *CronTriggerTester) monitor() error {
	for {
		select {
		case _, ok := <-c.triggerCh:
			if !ok {
				// Channel closed
				c.Lggr.Info("Trigger channel closed")
				return nil
			}
			CronHealthCheckTriggersCount.Inc()
		}
	}
}
