// Package capability holds the conventions shared between the core node and the
// standalone capability runner binaries it launches.
//
// A runner runs under the empty LOOP: the node supervises its liveness over
// go-plugin but exposes no RPCs to it, so CRE settings reach the runner through
// the filesystem instead. The node dumps each update to a conventional path and
// then hits the runner's reload endpoint; both processes share a container, so
// os.TempDir() resolves to the same place on either side.
package capability

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	// SettingsDirName names the directory, under os.TempDir(), that CRE settings
	// are dumped to.
	SettingsDirName = "cre_limits"

	// SettingsFileName is the name of the file CRE settings are dumped to, and
	// the path suffix of the runner's reload endpoint: /reload/<SettingsFileName>.
	//
	// A limit's effective value is resolved out of this same settings payload by
	// settings.Setting.GetOrDefault, so there is no separate limits file:
	// reloading this one reloads limits too.
	SettingsFileName = "settings.txt"

	// ReloadPathPrefix is the route prefix the runner serves reload requests on.
	ReloadPathPrefix = "/reload/"
)

// SettingsPath is the file CRE settings are dumped to. Both sides resolve it the
// same way, which is the whole contract: the node writes it, the runner reads it.
func SettingsPath() string {
	return filepath.Join(os.TempDir(), SettingsDirName, SettingsFileName)
}

// ReloadPath is the route the runner serves reload requests for the settings file
// on: /reload/settings.txt.
func ReloadPath() string { return ReloadPathPrefix + SettingsFileName }

// loadSettings reads the dumped settings file into s.
//
// A missing file is not an error: nothing has been dumped yet, so s keeps the
// getter it was built with and every limit resolves to its compiled-in default.
// That is the same state a LOOP starts in before its first update arrives.
func loadSettings(s *loop.AtomicSettings, path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read settings file %s: %w", path, err)
	}
	// Hash is left empty: the node owns the hash of an update, and what is on
	// disk is only ever a copy of one. Nothing downstream compares it.
	if err := s.Store(core.SettingsUpdate{Settings: string(b)}); err != nil {
		return fmt.Errorf("failed to apply settings from %s: %w", path, err)
	}
	return nil
}
