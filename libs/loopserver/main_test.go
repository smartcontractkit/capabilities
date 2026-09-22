package loopserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

type stubCapabilities struct {
	initErr error
}

func (s *stubCapabilities) Start(context.Context) error    { return nil }
func (s *stubCapabilities) Close() error                   { return nil }
func (s *stubCapabilities) HealthReport() map[string]error { return nil }
func (s *stubCapabilities) Name() string                   { return "stub" }
func (s *stubCapabilities) Ready() error                   { return nil }
func (s *stubCapabilities) Initialise(context.Context, core.StandardCapabilitiesDependencies) error {
	return s.initErr
}
func (s *stubCapabilities) Infos(context.Context) ([]capabilities.CapabilityInfo, error) {
	return nil, nil
}

func TestInitMetrics(t *testing.T) {
	im, err := newInitMetrics()
	require.NoError(t, err)
	require.NotNil(t, im)

	t.Run("records failure", func(t *testing.T) {
		assert.NotPanics(t, func() {
			im.recordInit(t.Context(), "evm", errors.New("boom"))
		})
	})

	t.Run("records success", func(t *testing.T) {
		assert.NotPanics(t, func() {
			im.recordInit(t.Context(), "stellar", nil)
		})
	})

	t.Run("nil receiver is a no-op", func(t *testing.T) {
		var im *initMetrics
		assert.NotPanics(t, func() {
			im.recordInit(t.Context(), "evm", nil)
		})
	})
}

func TestSettingsInterceptorInitialise(t *testing.T) {
	t.Run("init error is returned", func(t *testing.T) {
		i := &settingsInterceptor{
			StandardCapabilities: &stubCapabilities{initErr: errors.New("init failed")},
			lggr:                 logger.Nop(),
		}
		require.Error(t, i.Initialise(t.Context(), core.StandardCapabilitiesDependencies{}))
	})

	t.Run("init success", func(t *testing.T) {
		i := &settingsInterceptor{
			StandardCapabilities: &stubCapabilities{},
			lggr:                 logger.Nop(),
		}
		require.NoError(t, i.Initialise(t.Context(), core.StandardCapabilitiesDependencies{}))
	})
}
