package protofetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/modfile"
)

type lockFile struct {
	Dependencies []struct {
		Name       string
		URL        string
		CommitHash string `toml:"commit_hash"`
	}
}

type version struct {
	Version string
	Commit  string
	Time    time.Time
}

// toolchain is the go command and protofetch, which tests fake.
type toolchain interface {
	// resolve returns the version of module at ref, a version or a commit.
	resolve(ctx context.Context, dir, module, ref string) (version, error)
	goGet(ctx context.Context, dir, module, v string) error
	// lock requires the protofetch.toml of the module at dir to already pin dep at commit.
	lock(ctx context.Context, dir, dep, commit string) error
	lockAll(ctx context.Context, dir string) error
}

type requirement struct{ dir, version string }

// sync makes what the protofetch.toml at dir fetches be at the commits the Go modules generated from it are required
// at, by the go.mod of the first of modules that requires them. For what dir fetches itself, it moves the older of
// the two to the newer: that go.mod with go get, or protofetch.toml and protofetch.lock with protofetch update. What it
// fetches because what it fetches needs it is at the commit that needs it: a go.mod requiring it older is moved to
// it, and one requiring it newer is an error, as what needs it must move first.
func sync(ctx context.Context, dir string, tc toolchain, modules ...string) error {
	if len(modules) == 0 {
		return fmt.Errorf("%s: no go.mod to sync with", dir)
	}

	required := map[string]requirement{}
	for _, module := range modules {
		b, err := os.ReadFile(filepath.Join(module, "go.mod")) //nolint:gosec // A module's go.mod.
		if err != nil {
			return fmt.Errorf("%s has no go.mod for what %s fetches: %w", module, filepath.Join(dir, lockName), err)
		}

		mod, err := modfile.ParseLax("go.mod", b, nil)
		if err != nil {
			return err
		}

		for _, r := range mod.Require {
			if _, ok := required[r.Mod.Path]; !ok {
				required[r.Mod.Path] = requirement{module, r.Mod.Version}
			}
		}
	}

	deps, err := dependencies(dir)
	if err != nil {
		return err
	}

	unlocked, err := syncOwn(ctx, dir, tc, required, deps)
	if err != nil {
		return err
	}

	if unlocked {
		if err = tc.lockAll(ctx, dir); err != nil {
			return err
		}
	}

	// What was locked above may need others at new commits.
	if deps, err = dependencies(dir); err != nil {
		return err
	}

	return syncTransitive(ctx, dir, tc, required, deps)
}

// syncOwn syncs what dir fetches itself, reporting whether any of it is not locked yet.
func syncOwn(ctx context.Context, dir string, tc toolchain, required map[string]requirement, deps []dependency) (bool, error) {
	var errs []error
	unlocked := false
	for _, dep := range deps {
		if dep.Transitive {
			continue
		}

		goModule, req, mod, err := requiredVersion(ctx, dir, tc, required, dep)
		if err != nil || goModule == "" || mod.Commit == dep.CommitHash {
			errs = append(errs, err)
			continue
		}

		// Not locked yet, so fetched at the commit go.mod requires.
		if dep.CommitHash == "" {
			errs = append(errs, pin(dir, dep.Name, mod.Commit))
			unlocked = true
			continue
		}

		locked, err := tc.resolve(ctx, req.dir, goModule, dep.CommitHash)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", req.dir, err))
			continue
		}

		if locked.Time.After(mod.Time) {
			errs = append(errs, tc.goGet(ctx, req.dir, goModule, locked.Version))
			continue
		}

		if err = pin(dir, dep.Name, mod.Commit); err != nil {
			errs = append(errs, err)
			continue
		}

		errs = append(errs, tc.lock(ctx, dir, dep.Name, mod.Commit))
	}

	return unlocked, errors.Join(errs...)
}

// syncTransitive syncs what dir fetches because what it fetches needs it, which is at the commit that needs it.
func syncTransitive(ctx context.Context, dir string, tc toolchain, required map[string]requirement, deps []dependency) error {
	var errs []error
	for _, dep := range deps {
		if !dep.Transitive {
			continue
		}

		goModule, req, mod, err := requiredVersion(ctx, dir, tc, required, dep)
		if err != nil || goModule == "" || mod.Commit == dep.CommitHash {
			errs = append(errs, err)
			continue
		}

		locked, err := tc.resolve(ctx, req.dir, goModule, dep.CommitHash)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", req.dir, err))
			continue
		}

		if mod.Time.After(locked.Time) {
			errs = append(errs, fmt.Errorf("%s requires %s@%s, newer than %s, which what %s fetches needs: "+
				"move what it fetches to a revision that needs it", req.dir, goModule, req.version, locked.Version, dir))
			continue
		}

		errs = append(errs, tc.goGet(ctx, req.dir, goModule, locked.Version))
	}

	return errors.Join(errs...)
}

// requiredVersion returns the Go module generated from what dep fetches, the requirement of it and the version that
// is, or "" if no Go module is generated from it.
func requiredVersion(ctx context.Context, dir string, tc toolchain, required map[string]requirement, dep dependency) (string, requirement, version, error) {
	goModule, ok := repos[strings.TrimSuffix(dep.URL, "/")]
	if !ok {
		return "", requirement{}, version{}, nil
	}

	req, ok := required[goModule]
	if !ok {
		return "", requirement{}, version{}, fmt.Errorf("%s: %s fetches %s, but no go.mod requires %s", dir, lockName, dep.URL, goModule)
	}

	mod, err := tc.resolve(ctx, req.dir, goModule, req.version)
	if err != nil {
		return "", requirement{}, version{}, fmt.Errorf("%s: %w", req.dir, err)
	}

	return goModule, req, mod, nil
}

// dependency is in the protofetch.toml, or is Transitive: in the protofetch.lock only, as what the toml fetches needs
// it. CommitHash is empty if it is not locked yet.
type dependency struct {
	Name, URL, CommitHash string
	Transitive            bool
}

func dependencies(dir string) ([]dependency, error) {
	b, err := os.ReadFile(filepath.Join(dir, configName)) //nolint:gosec // A module's configuration.
	if err != nil {
		return nil, err
	}

	var config map[string]any
	if err = toml.Unmarshal(b, &config); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, configName), err)
	}

	var lock lockFile
	b, err = os.ReadFile(filepath.Join(dir, lockName)) //nolint:gosec // A module's lock file.
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err = toml.Unmarshal(b, &lock); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, lockName), err)
		}
	}

	locked := map[string]string{}
	for _, dep := range lock.Dependencies {
		locked[dep.Name] = dep.CommitHash
	}

	var deps []dependency
	for name, v := range config {
		table, ok := v.(map[string]any)
		if !ok {
			continue
		}

		url, _ := table["url"].(string)
		deps = append(deps, dependency{Name: name, URL: url, CommitHash: locked[name]})
	}

	for _, dep := range lock.Dependencies {
		if _, ok := config[dep.Name].(map[string]any); !ok {
			deps = append(deps, dependency{Name: dep.Name, URL: dep.URL, CommitHash: dep.CommitHash, Transitive: true})
		}
	}

	slices.SortFunc(deps, func(a, b dependency) int { return strings.Compare(a.Name, b.Name) })
	return deps, nil
}

// pin edits the revision in place, rather than rewriting the protofetch.toml, so its comments and layout are kept.
func pin(dir, dep, commit string) error {
	path := filepath.Join(dir, configName)
	b, err := os.ReadFile(path) //nolint:gosec // A module's configuration.
	if err != nil {
		return err
	}

	table := regexp.MustCompile(`(?ms)^\[` + regexp.QuoteMeta(dep) + `\]\s*$(.*?)(?:^\[|\z)`)
	loc := table.FindSubmatchIndex(b)
	if loc == nil {
		return fmt.Errorf("%s has no [%s]", path, dep)
	}

	body := b[loc[2]:loc[3]]
	revision := regexp.MustCompile(`(?m)^(revision\s*=\s*)"[^"]*"`)
	if len(revision.FindAllIndex(body, -1)) != 1 {
		return fmt.Errorf("%s: [%s] must have exactly one revision to update", path, dep)
	}

	updated := revision.ReplaceAll(body, []byte(`${1}"`+commit+`"`))
	out := append(append(append([]byte{}, b[:loc[2]]...), updated...), b[loc[3]:]...)
	return os.WriteFile(path, out, 0o644) //nolint:gosec // Source, readable like any other.
}

type commands struct{ protofetch string }

// binary prefers the protofetch pnpm installs at root, the version pnpm-lock.yaml pins, so go generate needs no PATH
// set up. Repos without one use protofetch from PATH.
func binary(root string) string {
	local := filepath.Join(root, "node_modules", ".bin", "protofetch")
	if _, err := os.Stat(local); err == nil {
		return local
	}

	return "protofetch"
}

func (commands) resolve(ctx context.Context, dir, module, ref string) (version, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", module+"@"+ref) //nolint:gosec // A module from go.mod or protofetch.lock.
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return version{}, fmt.Errorf("failed to resolve %s@%s: %w", module, ref, err)
	}

	var v struct {
		Version string
		Time    time.Time
		Origin  struct{ Hash string }
	}

	if err = json.Unmarshal(out, &v); err != nil {
		return version{}, err
	}

	if v.Origin.Hash == "" {
		return version{}, fmt.Errorf("the go command did not report the commit of %s@%s", module, ref)
	}

	return version{Version: v.Version, Commit: v.Origin.Hash, Time: v.Time}, nil
}

// goGet doesn't tidy, as sync runs before what is generated from it exists, and tidying would drop what only that
// needs.
func (commands) goGet(ctx context.Context, dir, module, v string) error {
	cmd := exec.CommandContext(ctx, "go", "get", module+"@"+v) //nolint:gosec // A module and version from go.mod or protofetch.lock.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: go get %s@%s failed: %w\n%s", dir, module, v, err, out)
	}

	return nil
}

func (c commands) lockAll(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, c.protofetch, "lock") //nolint:gosec // The repo's protofetch.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: protofetch lock failed: %w\n%s", dir, err, out)
	}

	return nil
}

func (c commands) lock(ctx context.Context, dir, dep, commit string) error {
	cmd := exec.CommandContext(ctx, c.protofetch, "update", dep, "--precise", commit) //nolint:gosec // A dependency from protofetch.lock.
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: protofetch update failed: %w\n%s", dir, err, out)
	}

	return nil
}
