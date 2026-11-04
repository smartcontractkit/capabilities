// Package protofetch is what the capability tools share about the protos modules fetch with protofetch.
package protofetch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const (
	configName = "protofetch.toml"
	lockName   = "protofetch.lock"
)

var repos = map[string]string{
	"github.com/smartcontractkit/chainlink-protos": "github.com/smartcontractkit/chainlink-protos/cre/go",
	"github.com/smartcontractkit/capabilities":     "github.com/smartcontractkit/capabilities/libs",
}

var goPackages = map[string]string{
	"sdk/v1alpha/sdk.proto":                      "github.com/smartcontractkit/chainlink-protos/cre/go/sdk",
	"tools/generator/v1alpha/cre_metadata.proto": "github.com/smartcontractkit/chainlink-protos/cre/go/tools/generator",
	"values/v1/values.proto":                     "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb",
}

// Root returns the directory of the protofetch.toml the module at dir fetches with: the nearest at or above it, so
// every module in a repo can share the one at its root.
func Root(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, configName)); err == nil {
			return d, nil
		}

		if d == filepath.Dir(d) {
			return "", fmt.Errorf("%s is not in a directory with a %s for the protos it imports", dir, configName)
		}
	}
}

// OutDir returns where protofetch fetches the protos of the module at dir to, its proto_out_dir, or "" if it does
// not fetch any.
func OutDir(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, configName)) //nolint:gosec // A module's configuration.
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	var config struct {
		ProtoOutDir string `toml:"proto_out_dir"`
	}

	if err = toml.Unmarshal(b, &config); err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Join(dir, configName), err)
	}

	if config.ProtoOutDir == "" {
		return "", fmt.Errorf("%s must set proto_out_dir", filepath.Join(dir, configName))
	}

	return filepath.Join(dir, filepath.FromSlash(config.ProtoOutDir)), nil
}
