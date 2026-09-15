package capability

// CRE settings, and the limits resolved out of them.
//
// A capability binary runs under the empty LOOP: the node supervises its liveness over go-plugin but
// exposes no RPCs to it, so settings reach it through the filesystem instead. The node dumps each
// update to a conventional path and then hits this process's reload endpoint; both share a
// container, so os.TempDir() resolves to the same place on either side.
//
// The constants below are that convention, and are a copy of the ones in standalone/capability
// rather than a reference to them - importing that package would be a cycle, since it reaches back
// into the bootstrapper that imports this one. They have to keep the same values: the node writes
// the file, this reads it.

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	// settingsDirName names the directory, under os.TempDir(), that CRE settings are dumped to.
	settingsDirName = "cre_limits"

	// settingsFileName is the name of the file CRE settings are dumped to, and the path suffix of
	// the reload endpoint: /reload/<settingsFileName>.
	//
	// A limit's effective value is resolved out of this same payload, so there is no separate limits
	// file: reloading this one reloads limits too.
	settingsFileName = "settings.txt"

	// reloadPathPrefix is the route prefix reload requests are served on.
	reloadPathPrefix = "/reload/"
)

// settingsPath is the file CRE settings are dumped to. Both sides resolve it the same way, which is
// the whole contract: the node writes it, this process reads it.
func settingsPath() string { return filepath.Join(os.TempDir(), settingsDirName, settingsFileName) }

// reloadPath is the route the node hits after dumping new settings: /reload/settings.txt.
func reloadPath() string { return reloadPathPrefix + settingsFileName }

// reloadHandler re-reads the settings file and swaps it in. 200 means every limit in this process
// now resolves against the new settings; 500 means none of them do and the previous settings are
// still in force.
func reloadHandler(lggr logger.Logger, settings *loop.AtomicSettings, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := loadSettings(settings, path); err != nil {
			lggr.Errorw("Failed to reload settings", "err", err, "path", path)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		lggr.Infow("Reloaded settings", "path", path)
		fmt.Fprintln(w, "ok")
	}
}

// newSettings builds this process's settings, seeded from the dumped file if the node has already
// written one.
func newSettings(lggr logger.Logger) (*loop.AtomicSettings, error) {
	s := &loop.AtomicSettings{Lggr: lggr}
	s.SetGetter(cresettings.DefaultGetter)

	if err := loadSettings(s, settingsPath()); err != nil {
		return nil, err
	}
	return s, nil
}

// loadSettings reads the dumped settings file into s.
//
// A missing file is not an error: nothing has been dumped yet, so s keeps the getter it was built
// with and every limit resolves to its compiled-in default. That is the same state a LOOP starts in
// before its first update arrives.
func loadSettings(s *loop.AtomicSettings, path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read settings file %s: %w", path, err)
	}

	// Hash is left empty: the node owns the hash of an update, and what is on disk is only ever a
	// copy of one. Nothing downstream compares it.
	if err := s.Store(core.SettingsUpdate{Settings: string(b)}); err != nil {
		return fmt.Errorf("failed to apply settings from %s: %w", path, err)
	}
	return nil
}

// newLimitsFactory builds the limits factory over settings.
//
// AtomicSettings is a settings.Getter rather than a settings.Registry, so the factory polls it
// rather than subscribing - which is why swapping the getter is enough to make every limit in the
// process follow a reload.
//
// The meter is read here rather than captured earlier, so it has to be built after telemetry has
// installed itself: an instrument resolves its meter once, and one made against the noop client
// records nothing for the life of the process.
func newLimitsFactory(lggr logger.Logger, settings *loop.AtomicSettings) limits.Factory {
	return limits.Factory{
		Settings: settings,
		Meter:    beholder.GetMeter(),
		Logger:   logger.Named(lggr, "Limits"),
	}
}
