package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cronV1 = `syntax = "proto3";

package capabilities.scheduler.cron.v1;

import "google/protobuf/timestamp.proto";
import "tools/generator/v1alpha/cre_metadata.proto";

message Config {
  string schedule = 1;
}

message Payload {
  google.protobuf.Timestamp scheduled_execution_time = 1;
}

service Cron {
  option (tools.generator.v1alpha.capability) = { mode: MODE_DON, capability_id: "cron-trigger@1.0.0" };

  rpc Trigger(Config) returns (stream Payload);
}
`

// cronV1NoSchedule is cronV1 without Config's only field.
const cronV1NoSchedule = `syntax = "proto3";

package capabilities.scheduler.cron.v1;

import "google/protobuf/timestamp.proto";

message Config {
}

message Payload {
  google.protobuf.Timestamp scheduled_execution_time = 1;
}

service Cron {
  rpc Trigger(Config) returns (stream Payload);
}
`

const cronV2 = `syntax = "proto3";

package capabilities.scheduler.cron.v2;

message Config {
  string schedule = 1;
}

service Cron {
  rpc Trigger(Config) returns (stream Config);
}
`

const cronV1Extra = `syntax = "proto3";

package capabilities.scheduler.cron.v1;

message Extra {
}
`

// metadata stands in for the cre_metadata.proto protofetch fetches, with what the protos here use.
const metadata = `syntax = "proto3";

package tools.generator.v1alpha;

import "google/protobuf/descriptor.proto";

message CapabilityMetadata {
  Mode mode = 1;
  string capability_id = 2;
}

enum Mode {
  MODE_UNSPECIFIED = 0;
  MODE_DON = 1;
}

extend google.protobuf.ServiceOptions {
  CapabilityMetadata capability = 50000;
}
`

// fetched is what each module with production protos has, as protofetch fetched it.
func fetched(module string) map[string]string {
	return map[string]string{
		module + "protofetch.toml": "proto_out_dir = \"protos/.deps\"\n",
		module + "protofetch.lock": "version = 2\n",
		module + "go.mod":          "module example.com/capability\n",
		module + "protos/.deps/tools/generator/v1alpha/cre_metadata.proto": metadata,
	}
}

// checkout writes files, by path from the checkout's root, to a new checkout, with what protofetch fetches for
// each module with production protos.
func checkout(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	all := map[string]string{}
	for name, content := range files {
		all[name] = content
		if i := strings.Index(name, "protos/production/"); i >= 0 {
			for k, v := range fetched(name[:i]) {
				all[k] = v
			}
		}
	}
	for name, content := range all {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return root
}

const (
	cronDir    = "scheduler/cron/protos/production/"
	cronV1Path = cronDir + "capabilities/scheduler/cron/v1/trigger.proto"
	cronV2Path = cronDir + "capabilities/scheduler/cron/v2/trigger.proto"
	cronV1Alt  = "scheduler/other/protos/production/capabilities/scheduler/cron/v1/trigger.proto"
)

func TestCheck(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("buf is not installed")
	}

	base := map[string]string{cronV1Path: cronV1, cronDir + "capabilities/scheduler/cron/v1/extra.proto": cronV1Extra}
	for name, tt := range map[string]struct {
		base    map[string]string
		next    map[string]string
		wantErr string
	}{
		"unchanged":         {next: base},
		"new version added": {next: with(base, cronV2Path, cronV2)},
		"moved capability": {next: map[string]string{
			"scheduler/moved/protos/production/capabilities/scheduler/cron/v1/trigger.proto": cronV1,
			"scheduler/moved/protos/production/capabilities/scheduler/cron/v1/extra.proto":   cronV1Extra,
		}},
		"field removed":     {next: with(base, cronV1Path, cronV1NoSchedule), wantErr: "FIELD_NO_DELETE"},
		"removed unretired": {next: map[string]string{cronV2Path: cronV2}, wantErr: "add it to " + retiredFile},
		"retired":           {next: map[string]string{cronV2Path: cronV2, retiredFile: "# Moves to v2.\ncapabilities.scheduler.cron.v1\n"}},
		"retired not gone":  {next: with(base, retiredFile, "capabilities.scheduler.cron.v1\n"), wantErr: "still exists"},
		"retired partially": {next: map[string]string{cronDir + "capabilities/scheduler/cron/v1/extra.proto": cronV1Extra, retiredFile: "capabilities.scheduler.cron.v1\n"}, wantErr: "still exists"},
		"unretired":         {base: with(base, retiredFile, "capabilities.scheduler.cron.v0\n"), next: base, wantErr: "stays retired"},
		"still retired":     {base: with(base, retiredFile, "capabilities.scheduler.cron.v0\n"), next: with(base, retiredFile, "capabilities.scheduler.cron.v0\n")},
		// The package remains in extra.proto, so trigger.proto going is an ordinary break.
		"file removed": {
			next:    map[string]string{cronDir + "capabilities/scheduler/cron/v1/extra.proto": cronV1Extra},
			wantErr: "FILE_NO_DELETE",
		},
		"duplicate path": {next: with(base, cronV1Alt, cronV1), wantErr: "is the production proto of both"},
		// Only protos/production directories are production protos.
		"not a production dir": {next: with(base, "scheduler/cron/production/capabilities/x/v1/x.proto", "not a proto")},
	} {
		t.Run(name, func(t *testing.T) {
			b := tt.base
			if b == nil {
				b = base
			}

			err := check(t.Context(), checkout(t, b), checkout(t, tt.next))
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
			// Imports are compared only where they are used, so the base protos are never findings.
			assert.NotContains(t, err.Error(), "tools/generator")
		})
	}
}

// with is files with name set to content.
func with(files map[string]string, name, content string) map[string]string {
	out := map[string]string{name: content}
	for k, v := range files {
		if k != name {
			out[k] = v
		}
	}
	return out
}

func TestCheckNoBaseline(t *testing.T) {
	// A repo without production protos yet has nothing to break.
	require.NoError(t, check(t.Context(), checkout(t, nil), checkout(t, map[string]string{cronV1Path: cronV1})))
}

// TestCheckImportsChanged checks the protos production protos import are not themselves checked: they come from
// elsewhere, and are checked there.
func TestCheckImportsChanged(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("buf is not installed")
	}

	base := checkout(t, map[string]string{cronV1Path: cronV1})
	next := checkout(t, map[string]string{cronV1Path: cronV1})
	changed := strings.Replace(metadata, "MODE_DON = 1;", "MODE_DON = 1;\n  MODE_NODE = 2;", 1)
	require.NoError(t, os.WriteFile(filepath.Join(next, cronDir, "../.deps/tools/generator/v1alpha/cre_metadata.proto"), []byte(changed), 0o600))
	require.NoError(t, check(t.Context(), base, next))
}

// TestCheckAllRetired checks a checkout can retire every production package it has.
func TestCheckAllRetired(t *testing.T) {
	base := checkout(t, map[string]string{cronV1Path: cronV1})
	require.NoError(t, check(t.Context(), base, checkout(t, map[string]string{retiredFile: "capabilities.scheduler.cron.v1\n"})))
}

// TestCheckNestedPackage checks production protos in a package of a module above it, such as a capability
// scaffolded into libs, import what that module fetched.
func TestCheckNestedPackage(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("buf is not installed")
	}

	nested := map[string]string{"libs/capability/internal/example/capability/protos/production/capabilities/scheduler/cron/v1/trigger.proto": cronV1}
	base, next := t.TempDir(), t.TempDir()
	for _, root := range []string{base, next} {
		for name, content := range nested {
			writeFile(t, root, name, content)
		}
		for name, content := range fetched("libs/") {
			writeFile(t, root, name, content)
		}
	}
	require.NoError(t, check(t.Context(), base, next))
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestCheckNotFetched(t *testing.T) {
	next := checkout(t, map[string]string{cronV1Path: cronV1})
	require.NoError(t, os.RemoveAll(filepath.Join(next, "scheduler/cron/protos/.deps")))
	assert.ErrorContains(t, check(t.Context(), checkout(t, nil), next), "run protofetch fetch --locked")
}

func TestReadRetired(t *testing.T) {
	root := checkout(t, map[string]string{retiredFile: "# Old versions.\ncapabilities.scheduler.cron.v1\n\n  capabilities.http.v1alpha # Moved to v1.\n"})
	retired, err := readRetired(root)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"capabilities.scheduler.cron.v1": true, "capabilities.http.v1alpha": true}, retired)

	retired, err = readRetired(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, retired)
}
