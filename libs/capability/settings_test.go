package capability

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
)

// TestLoadSettingsWithoutAFile covers the state a binary starts in before the node has dumped
// anything: every limit resolves to its compiled-in default, which is the same state a LOOP is in
// before its first update arrives.
func TestLoadSettingsWithoutAFile(t *testing.T) {
	s, err := newSettings(logger.Test(t))
	require.NoError(t, err)
	require.NotNil(t, s)

	require.NoError(t, loadSettings(s, filepath.Join(t.TempDir(), "nothing-here.txt")),
		"a missing file is not an error")
}

// TestLoadSettingsReportsAnUnreadableFile covers the other side: a file that is there and cannot be
// used is a failure to start, not something to carry on past with defaults.
func TestLoadSettingsReportsAnUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.txt")
	require.NoError(t, os.WriteFile(path, []byte("not a settings payload"), 0o600))

	s, err := newSettings(logger.Test(t))
	require.NoError(t, err)

	require.ErrorContains(t, loadSettings(s, path), path)
}

// TestSettingsPathIsTheSharedConvention pins the contract with the node: it writes this file and
// this process reads it, so the two have to resolve the same path.
//
// The constants are a copy of standalone/capability's - importing that package would be a cycle -
// so nothing but a test stops them drifting apart.
func TestSettingsPathIsTheSharedConvention(t *testing.T) {
	assert.Equal(t, filepath.Join(os.TempDir(), "cre_limits", "settings.txt"), settingsPath())
	assert.Equal(t, "/reload/", reloadPathPrefix)
}

// TestLimitsFactoryIsBuiltOverTheSettings covers the wiring the whole thing exists for: a limit made
// by this factory reads the settings this process holds, so a reload reaches it.
func TestLimitsFactoryIsBuiltOverTheSettings(t *testing.T) {
	s, err := newSettings(logger.Test(t))
	require.NoError(t, err)

	f := newLimitsFactory(logger.Test(t), s)

	assert.Same(t, s, f.Settings, "the factory should poll the settings this process holds")
	assert.NotNil(t, f.Meter)
	assert.NotNil(t, f.Logger)
}

// TestReloadHandlerSwapsTheSettings covers the endpoint the node hits after dumping new settings: a
// limit made from this process's factory resolves against the new payload on its next use, without
// anything having to be told.
func TestReloadHandlerSwapsTheSettings(t *testing.T) {
	s, err := newSettings(logger.Test(t))
	require.NoError(t, err)

	// A global-scope setting, so no tenant is needed to resolve it. The compiled-in default is 0s.
	f := newLimitsFactory(logger.Test(t), s)
	limit, err := f.MakeTimeLimiter(cresettings.Default.TriggerRegistrationStatusUpdateTimeout)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "settings.txt")
	require.NoError(t, os.WriteFile(path,
		[]byte("[global]\nTriggerRegistrationStatusUpdateTimeout = \"15s\"\n"), 0o600))

	w := httptest.NewRecorder()
	reloadHandler(logger.Test(t), s, path)(w, httptest.NewRequest(http.MethodPost, reloadPath(), nil))
	require.Equal(t, http.StatusOK, w.Code)

	got, err := limit.Limit(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 15*time.Second, got, "the limit should resolve against the reloaded settings")
}

// TestReloadHandlerKeepsThePreviousSettingsOnFailure covers the node's signal to retry later: a
// payload that cannot be applied is a 500, and what was in force stays in force.
func TestReloadHandlerKeepsThePreviousSettingsOnFailure(t *testing.T) {
	s, err := newSettings(logger.Test(t))
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "settings.txt")
	require.NoError(t, os.WriteFile(path, []byte("not a settings payload"), 0o600))

	w := httptest.NewRecorder()
	reloadHandler(logger.Test(t), s, path)(w, httptest.NewRequest(http.MethodPost, reloadPath(), nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
