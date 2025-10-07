package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/pelletier/go-toml/v2"

	"github.com/smartcontractkit/capabilities/mock/internal"
	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var _ loop.StandardCapabilities = (*MockServer)(nil)

type Mocks struct {
	ID          string `toml:"id"`
	Description string `toml:"description"`
	Type        string `toml:"type"`
}
type Config struct {
	Port         int     `toml:"port"`
	DefaultMocks []Mocks `toml:"defaultMocks"`
}

type MockServer struct {
	Lggr         logger.Logger
	MockRegistry *internal.MockRegistry
}

func (s *MockServer) Start(ctx context.Context) error {
	return nil
}

func (s *MockServer) Close() error {
	return nil
}

func (s *MockServer) Ready() error {
	return nil
}

func (s *MockServer) HealthReport() map[string]error {
	return nil
}

func (s *MockServer) Name() string {
	return s.Lggr.Name()
}

func (s *MockServer) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	if len(dependencies.Config) < 1 {
		return errors.New("missing config")
	}
	var mockConfig Config
	err := toml.Unmarshal([]byte(dependencies.Config), &mockConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %s %w", dependencies.Config, err)
	}

	if mockConfig.Port == 0 {
		return errors.New("must specify a port number")
	}

	s.MockRegistry = internal.NewMockRegistry(s.Lggr, dependencies.CapabilityRegistry)

	for _, m := range mockConfig.DefaultMocks {
		capType := utils.ToMockServerEnum(capabilities.CapabilityType(m.Type))
		if capType == 0 {
			s.Lggr.Errorf("could not start mock capabilitie %s unknown capability type %s", m.ID, m.Type)
		}
		_, err = s.MockRegistry.CreateCapability(context.Background(), &pb.CapabilityInfo{
			ID:             m.ID,
			CapabilityType: capType,
			Description:    m.Description,
			IsLocal:        true,
		})
		if err != nil {
			return err
		}
	}

	go func() {
		err2 := s.MockRegistry.Start(mockConfig.Port)
		if err2 != nil {
			panic(err2)
		}
	}()

	return nil
}

func (s *MockServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	infos := make([]capabilities.CapabilityInfo, 0)
	for _, c := range s.MockRegistry.Triggers {
		infos = append(infos, c.CapabilityInfo)
	}
	for _, c := range s.MockRegistry.Executables {
		infos = append(infos, c.CapabilityInfo)
	}

	return infos, nil
}

func New(lggr logger.Logger) *MockServer {
	return &MockServer{
		Lggr: logger.Sugared(lggr).Named("MockServer"),
	}
}
