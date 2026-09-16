package capcommon

import (
	"context"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// AverageRequestTimeout returns the average RequestTimeout configured across
// capabilityID's CapabilityMethodConfig entries for donID. Method configs for
// WriteReport and LogTrigger methods are excluded — their timeout semantics
// differ from regular executable methods. If the config can't be fetched or no
// eligible RemoteExecutableConfig.RequestTimeout values are found, it returns
// fallback.
func AverageRequestTimeout(ctx context.Context, registry core.CapabilitiesRegistry, capabilityID string, donID uint32, fallback time.Duration, lggr logger.Logger) time.Duration {
	if registry == nil {
		return fallback
	}

	cfg, err := WithPollingRetry(ctx, lggr, func(ctx context.Context) (capabilities.CapabilityConfiguration, error) {
		return registry.ConfigForCapability(ctx, capabilityID, donID)
	})
	if err != nil {
		lggr.Errorw("failed getting config for capability", "capabilityID", capabilityID, "error", err)
		return fallback
	}

	var total time.Duration
	var count int
	for method, methodCfg := range cfg.CapabilityMethodConfig {
		if isNonReadMethod(method) {
			continue
		}
		if methodCfg.RemoteExecutableConfig == nil || methodCfg.RemoteExecutableConfig.RequestTimeout == 0 {
			continue
		}
		total += methodCfg.RemoteExecutableConfig.RequestTimeout
		count++
	}
	if count == 0 {
		return fallback
	}
	return total / time.Duration(count)
}

func isNonReadMethod(method string) bool {
	switch method {
	case "WriteReport", "LogTrigger":
		return true
	default:
		return false
	}
}
