package service

import (
	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/scheduler/cron/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

// ConfigNamespace is the namespace the cron capability's Config is set under.
const ConfigNamespace = "cron"

// init provides Config however the capability is run, so whoever embeds it gets its defaults without providing it.
func init() {
	capability.Injectors.MustProvideConfig(ConfigNamespace, &Config{})
}

var _ capability.Capability = (*protos.CronServer)(nil)

// New builds the cron capability from what the run provides, for capability.Run. An OrgResolver is
// optional; without one, organization IDs are not fetched.
func New(i do.Injector) (*protos.CronServer, error) {
	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return nil, err
	}

	clock, err := do.Invoke[clockwork.Clock](i)
	if err != nil {
		return nil, err
	}

	limitsFactory, err := do.Invoke[limits.Factory](i)
	if err != nil {
		return nil, err
	}

	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return nil, err
	}

	orgResolver, _ := do.Invoke[orgresolver.OrgResolver](i)
	capCfg, _ := do.Invoke[capability.CapabilitiesConfig](i)
	svc, err := NewService(lggr, clock, limitsFactory, *cfg, orgResolver, capCfg.ShardIndex)
	if err != nil {
		return nil, err
	}

	return protos.NewCronServer(svc), nil
}
