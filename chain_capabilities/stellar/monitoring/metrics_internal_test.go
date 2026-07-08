package monitoring

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

func TestNs(t *testing.T) {
	t.Parallel()
	require.Equal(t, "stellar_capability_read_contract_success", ns("read_contract_success"))
}

func TestNewMetrics_ConstructorError(t *testing.T) {
	orig := newMetricsCapBasic
	defer func() { newMetricsCapBasic = orig }()

	newMetricsCapBasic = func(capmonitoring.MetricsInfoCapBasic) (capmonitoring.MetricsCapBasic, error) {
		return capmonitoring.MetricsCapBasic{}, errors.New("instrument registration failed")
	}

	_, err := NewMetrics()
	require.Error(t, err)
	require.Contains(t, err.Error(), "instrument registration failed")
}

func TestNewMetrics_RegistrationErrors(t *testing.T) {
	metricNames := []string{
		"read_contract_success",
		"read_contract_error",
		"write_report_success",
		"write_report_error",
		"write_report_duplicate_tx",
		"write_report_tx_info_retrieval_error",
		"write_report_successful_early_return",
		"write_report_invalid_transmission_state",
	}

	orig := newMetricsCapBasic
	defer func() { newMetricsCapBasic = orig }()

	for i, metricName := range metricNames {
		t.Run(metricName, func(t *testing.T) {
			call := 0
			newMetricsCapBasic = func(info capmonitoring.MetricsInfoCapBasic) (capmonitoring.MetricsCapBasic, error) {
				call++
				if call == i+1 {
					return capmonitoring.MetricsCapBasic{}, errors.New("registration failed")
				}
				return orig(info)
			}

			_, err := NewMetrics()
			require.Error(t, err)
			require.Contains(t, err.Error(), metricName)
		})
	}
}
