package service

import (
	"context"
	"errors"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// ConfigNamespace is the namespace the capability capability's Config is set under.
const ConfigNamespace = "capability"

// Config is how the node operator configures the capability capability.
type Config struct{}

// init provides Config however the capability is run, so whoever embeds it gets its defaults without providing it.
func init() {
	capability.Injectors.MustProvideConfig(ConfigNamespace, &Config{})
}

var _ capability.Capability = (*protos.ClientServer)(nil)

// dependencies is what New takes from the run. Add a field, tagged `do:""`, for each thing the capability
// needs; tests override them with capability.WithProvider.
type dependencies struct {
	Logger logger.Logger `do:""`
	Config *Config       `do:""`
}

// New builds the capability capability from what the run provides, for capability.Run.
func New(i do.Injector) (*protos.ClientServer, error) {
	deps, err := do.InvokeStruct[dependencies](i)
	if err != nil {
		return nil, err
	}

	return protos.NewClientServer(newService(deps)), nil
}

// service implements the capability capability.
// TODO: replace the echoes with what the capability does.
type service struct {
	// Service gives Start, Close, Ready and HealthReport that follow the lifecycle: not ready until started,
	// unhealthy once closed. Hook work into them through services.Config's Start and Close rather than
	// overriding them, so health keeps reporting the truth.
	services.Service
	cfg Config
}

var _ protos.ClientCapability = (*service)(nil)

func newService(deps dependencies) *service {
	return &service{Service: services.Config{Name: "Client"}.NewService(deps.Logger), cfg: *deps.Config}
}

func (s *service) Description() string {
	return "The capability capability"
}

// RegisterTrigger sends one event echoing the trigger's config.
func (s *service) RegisterTrigger(_ context.Context, triggerID string, _ capabilities.RequestMetadata, input *protos.TriggerConfig) (<-chan capabilities.TriggerAndId[*protos.TriggerEvent], caperrors.Error) {
	events := make(chan capabilities.TriggerAndId[*protos.TriggerEvent], 1)
	events <- capabilities.TriggerAndId[*protos.TriggerEvent]{Id: triggerID, Trigger: &protos.TriggerEvent{Message: input.Message}}
	return events, nil
}

func (s *service) UnregisterTrigger(context.Context, string, capabilities.RequestMetadata, *protos.TriggerConfig) caperrors.Error {
	return nil
}

// AckEvent is the engine confirming it has an event, so the capability can stop resending it.
// TODO: deliver events through a capabilities.BaseTriggerCapability, which resends until acked, and ack through it
// here. It needs the node's capabilities.EventStore, which capability.Run does not provide yet.
func (s *service) AckEvent(context.Context, string, string, string) caperrors.Error {
	return caperrors.NewPrivateSystemError(errors.New("AckEvent is not implemented"), caperrors.Unimplemented)
}

// Execute replies with the request's message.
func (s *service) Execute(_ context.Context, _ capabilities.RequestMetadata, input *protos.Request) (*capabilities.ResponseAndMetadata[*protos.Reply], caperrors.Error) {
	return &capabilities.ResponseAndMetadata[*protos.Reply]{Response: &protos.Reply{Message: input.Message}}, nil
}
