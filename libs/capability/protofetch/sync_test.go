package protofetch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	protosModule = "github.com/smartcontractkit/chainlink-protos/cre/go"

	older        = "v0.0.0-20261001000000-111111111111"
	olderCommit  = "1111111111111111111111111111111111111111"
	newer        = "v0.0.0-20261013212015-e622e8b83b6b"
	newerCommit  = "e622e8b83b6b28c99a9c12270e4d5a39c87381af"
	config       = "name = \"x\"\nproto_out_dir = \"protos/.deps\"\n\n[chainlink-protos]\nurl = \"github.com/smartcontractkit/chainlink-protos\"\nrevision = \"%s\"\ncontent_roots = [\"/cre\"]\n"
	lockTemplate = "version = 2\n\n[[dependencies]]\nname = \"chainlink-protos\"\nurl = \"github.com/smartcontractkit/chainlink-protos\"\nrevision = \"%s\"\ncommit_hash = \"%s\"\n\n[[dependencies]]\nname = \"other\"\nurl = \"github.com/example/other\"\ncommit_hash = \"0000000000000000000000000000000000000000\"\n"
)

// fake is the go command and protofetch for the two versions above.
type fake struct {
	got, gotIn, locked []string
	lockedAll          int
}

func (f *fake) resolve(_ context.Context, _, module, ref string) (version, error) {
	if module != protosModule {
		return version{}, errors.New("unexpected module " + module)
	}

	switch ref {
	case older, olderCommit:
		return version{Version: older, Commit: olderCommit, Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, nil
	case newer, newerCommit:
		return version{Version: newer, Commit: newerCommit, Time: time.Date(2026, 10, 13, 21, 20, 15, 0, time.UTC)}, nil
	}

	return version{}, errors.New("unknown " + ref)
}

func (f *fake) goGet(_ context.Context, dir, module, v string) error {
	f.got = append(f.got, module+"@"+v)
	f.gotIn = append(f.gotIn, dir)
	return nil
}

func (f *fake) lockAll(context.Context, string) error {
	f.lockedAll++
	return nil
}

func (f *fake) lock(_ context.Context, _, dep, commit string) error {
	f.locked = append(f.locked, dep+"@"+commit)
	return nil
}

func module(t *testing.T, v, commit string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	write("go.mod", "module example.com/capability\n\ngo 1.27.3\n\nrequire "+protosModule+" "+v+"\n")
	write("protofetch.toml", fmt.Sprintf(config, commit))
	write("protofetch.lock", fmt.Sprintf(lockTemplate, commit, commit))
	return dir
}

func TestSyncRequiresTheModule(t *testing.T) {
	dir := module(t, newer, newerCommit)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/capability\n"), 0o600))
	assert.ErrorContains(t, sync(t.Context(), dir, &fake{}, dir), "no go.mod requires "+protosModule)
}

func TestSyncNoGoMod(t *testing.T) {
	dir := module(t, newer, newerCommit)
	require.NoError(t, os.Remove(filepath.Join(dir, "go.mod")))
	assert.ErrorContains(t, sync(t.Context(), dir, &fake{}, dir), "has no go.mod")
}

// TestSyncLockBehind checks a lock older than go.mod is moved to go.mod's commit.
func TestSyncLockBehind(t *testing.T) {
	dir := module(t, newer, olderCommit)
	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir))

	assert.Empty(t, f.got)
	assert.Equal(t, []string{"chainlink-protos@" + newerCommit}, f.locked)
	b, err := os.ReadFile(filepath.Join(dir, "protofetch.toml"))
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(config, newerCommit), string(b), "only the revision changes")
}

// TestSyncGoModBehind checks a go.mod older than the lock is moved to the lock's commit.
func TestSyncGoModBehind(t *testing.T) {
	dir := module(t, older, newerCommit)
	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir))

	assert.Equal(t, []string{protosModule + "@" + newer}, f.got)
	assert.Empty(t, f.locked)
	b, err := os.ReadFile(filepath.Join(dir, "protofetch.toml"))
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(config, newerCommit), string(b), "the lock is not touched")
}

func TestSyncInSync(t *testing.T) {
	dir := module(t, newer, newerCommit)
	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir))
	assert.Empty(t, f.got)
	assert.Empty(t, f.locked)
}

func TestPinNeedsOneRevision(t *testing.T) {
	dir := module(t, newer, olderCommit)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "protofetch.toml"), []byte("[chainlink-protos]\nurl = \"x\"\n"), 0o600))
	assert.ErrorContains(t, pin(dir, "chainlink-protos", newerCommit), "exactly one revision")
	assert.ErrorContains(t, pin(dir, "missing", newerCommit), "has no [missing]")
}

// TestResolve checks the go command resolves what go.mod requires to its commit.
func TestResolve(t *testing.T) {
	got, err := commands{}.resolve(t.Context(), ".", protosModule, newer)
	if err != nil {
		t.Skipf("the module is not available: %v", err)
	}

	assert.Equal(t, newerCommit, got.Commit)
	assert.Equal(t, newer, got.Version)
	assert.False(t, got.Time.IsZero())
}

// TestSyncOtherModule checks a repo whose Go is required by another module is kept in step with that module's go.mod.
func TestSyncOtherModule(t *testing.T) {
	dir := module(t, older, newerCommit)
	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/generate\n\nrequire "+protosModule+" "+older+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/capability\n"), 0o600))

	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir, other))
	assert.Equal(t, []string{protosModule + "@" + newer}, f.got)
	assert.Equal(t, []string{other}, f.gotIn)
}

// TestSyncFirstRun checks a module without a protofetch.lock yet is pinned to what go.mod requires, then locked.
func TestSyncFirstRun(t *testing.T) {
	dir := module(t, newer, "")
	require.NoError(t, os.Remove(filepath.Join(dir, "protofetch.lock")))

	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir))
	assert.Equal(t, 1, f.lockedAll)
	assert.Empty(t, f.got)
	b, err := os.ReadFile(filepath.Join(dir, "protofetch.toml"))
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(config, newerCommit), string(b))
}

// transitive writes a module whose protofetch.toml fetches only something that needs chainlink-protos, at commit.
func transitive(t *testing.T, v, commit string) string {
	t.Helper()
	dir := module(t, v, commit)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "protofetch.toml"), []byte(
		"name = \"x\"\nproto_out_dir = \".deps\"\n\n[other]\nurl = \"github.com/example/other\"\nrevision = \"0000000000000000000000000000000000000000\"\n"), 0o600))
	return dir
}

// TestSyncTransitiveGoModBehind checks a go.mod older than what what is fetched needs is moved to it.
func TestSyncTransitiveGoModBehind(t *testing.T) {
	dir := transitive(t, older, newerCommit)
	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir))
	assert.Equal(t, []string{protosModule + "@" + newer}, f.got)
	assert.Empty(t, f.locked, "what is fetched decides")
}

// TestSyncTransitiveGoModAhead checks a go.mod newer than what what is fetched needs fails, as it can't be moved back.
func TestSyncTransitiveGoModAhead(t *testing.T) {
	dir := transitive(t, newer, olderCommit)
	f := &fake{}
	err := sync(t.Context(), dir, f, dir)
	assert.ErrorContains(t, err, "newer than "+older)
	assert.Empty(t, f.got)
	assert.Empty(t, f.locked)
}

func TestSyncTransitiveInSync(t *testing.T) {
	dir := transitive(t, newer, newerCommit)
	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, dir))
	assert.Empty(t, f.got)
}

// TestSyncFirstModuleWins checks the first of modules that requires a Go module is the one synced.
func TestSyncFirstModuleWins(t *testing.T) {
	dir := module(t, older, newerCommit)
	first := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(first, "go.mod"), []byte("module example.com/first\n\nrequire "+protosModule+" "+newer+"\n"), 0o600))
	f := &fake{}
	require.NoError(t, sync(t.Context(), dir, f, first, dir))
	assert.Empty(t, f.got, "first requires what is locked")
}

func TestSyncNoModules(t *testing.T) {
	assert.ErrorContains(t, sync(t.Context(), module(t, newer, newerCommit), &fake{}), "no go.mod")
}

func TestRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, configName), []byte("name = \"x\"\n"), 0o600))
	nested := filepath.Join(root, "scheduler", "cron")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	got, err := Root(nested)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	gotReal, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	assert.Equal(t, want, gotReal)

	_, err = Root(t.TempDir())
	assert.ErrorContains(t, err, "is not in a directory with a "+configName)
}

func TestFlatten(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "capabilities", "scheduler", "cron", "v1")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "trigger.pb.go"), []byte("package cron\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "capabilities", "kept"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "capabilities", "kept", "x"), nil, 0o600))

	require.NoError(t, flatten(dir, "capabilities/scheduler/cron/v1"))
	assert.FileExists(t, filepath.Join(dir, "trigger.pb.go"))
	assert.NoDirExists(t, filepath.Join(dir, "capabilities", "scheduler"))
	assert.FileExists(t, filepath.Join(dir, "capabilities", "kept", "x"), "what flatten did not empty is kept")

	require.NoError(t, flatten(dir, "missing/v1"), "nothing generated there")
}
