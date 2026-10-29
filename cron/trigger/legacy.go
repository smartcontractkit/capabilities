package trigger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// LegacyService is the Service for the LOOP plugin, where the node passes the config and the
// OrgResolver through Initialise.
type LegacyService struct {
	*Service
}

var _ server.CronCapability = &LegacyService{}

func NewLegacyService(lggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory) (*LegacyService, error) {
	s, err := NewService(lggr, clock, limitsFactory, Config{})
	if err != nil {
		return nil, err
	}
	return &LegacyService{Service: s}, nil
}

func (s *LegacyService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	s.lggr.Debugw("Initialising cron trigger capability", "serviceName", ServiceName)

	if len(dependencies.Config) > 0 {
		var cronConfig Config
		if err := json.Unmarshal([]byte(dependencies.Config), &cronConfig); err != nil {
			return fmt.Errorf("failed to unmarshal config: %s %w", dependencies.Config, err)
		}
		if cronConfig.FastestScheduleIntervalSeconds > 0 {
			limiter, err := newFastestScheduleInterval(s.limitsFactory, cronConfig)
			if err != nil {
				return err
			}
			if err := s.fastestScheduleInterval.Close(); err != nil {
				s.lggr.Warnw("Failed to close the default fastest schedule interval limiter", "err", err)
			}
			s.fastestScheduleInterval = limiter
		}
	}

	s.shardIndex = dependencies.ShardIndex
	s.orgResolver = dependencies.OrgResolver
	if s.orgResolver == nil {
		s.lggr.Warn("OrgResolver is nil, cron capability will not be able to fetch organization ID")
	}

	if err := s.Start(ctx); err != nil {
		return fmt.Errorf("error when starting trigger service: %w", err)
	}

	return nil
}
