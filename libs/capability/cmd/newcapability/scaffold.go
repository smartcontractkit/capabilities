package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/smartcontractkit/capabilities/libs/capability/cmd/newcapability/options"
	"github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc-gen-cre/production"
)

const (
	repoModule = "github.com/smartcontractkit/capabilities"
	libsModule = repoModule + "/libs"
)

//go:embed templates
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// identifier is what a category or name must be, as they are used in module paths and proto packages.
var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// scaffold is a capability to create. A trigger alone is served from trigger.proto; with an action, the
// capability is one service in client.proto that has both, as the EVM client does.
type scaffold struct {
	options.Options

	// Module, GoVersion and Libs are filled in by run: the module's path, which follows where it is in the
	// repo, the go version libs requires, and the path from the module to libs, which it is replaced with.
	Module, GoVersion, Libs string
}

// Package is the Go package that implements the service, whatever kind it is, so service.New is always what
// main runs.
func (s *scaffold) Package() string {
	return "service"
}

// Proto is the proto file's name: the service is Client whenever it has an action.
func (s *scaffold) Proto() string {
	if s.Action {
		return "client"
	}
	return "trigger"
}

// Project is the nx project's name, which must be unique in the repo: the module's path in it, dashed.
func (s *scaffold) Project() string {
	return strings.ReplaceAll(strings.TrimPrefix(s.Module, repoModule+"/"), "/", "-")
}

func (s *scaffold) Service() string {
	if s.Action {
		return "Client"
	}
	return strings.ToUpper(s.Name[:1]) + s.Name[1:]
}

func (s *scaffold) CapabilityID() string {
	if s.Action {
		return s.Name + "@1.0.0"
	}
	return s.Name + "-trigger@1.0.0"
}

func (s *scaffold) run(ctx context.Context) error {
	if !s.Trigger && !s.Action {
		return errors.New("at least one of --trigger or --action is required")
	}
	for _, v := range []string{s.Category, s.Name} {
		if !identifier.MatchString(v) {
			// It is part of the module path, the proto package and the capability ID: no case to clash on
			// case-insensitive filesystems or be escaped in the module path, and no separators protobuf rejects.
			return fmt.Errorf("%q must be lowercase letters and digits, starting with a letter", v)
		}
	}

	libsDir, err := s.findLibs(ctx)
	if err != nil {
		return err
	}
	if s.OutDir == "" {
		s.OutDir = filepath.Dir(libsDir)
	}

	dir := filepath.Join(s.OutDir, s.Category, s.Name)
	if _, err = os.Stat(dir); err == nil {
		if !s.Force {
			return fmt.Errorf("%s already exists, use --force to replace it", dir)
		}
		if err = s.clean(dir); err != nil {
			return err
		}
	}
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Resolved, so the replace is relative to where the module really is, not a symlink to it.
	if dir, err = filepath.Abs(dir); err != nil {
		return err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return err
	}
	if err = s.place(dir, libsDir); err != nil {
		return err
	}
	if !s.NoModule {
		// Everything the module needs comes through libs, so starting from its sums keeps tidy from verifying
		// each of them again.
		sums, err := os.ReadFile(filepath.Join(libsDir, "go.sum"))
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(dir, "go.sum"), sums, 0o644); err != nil { //nolint:gosec // Source in the --out-dir the user chose.
			return err
		}
	}

	for path, name := range s.files() {
		if err = s.write(filepath.Join(dir, path), name); err != nil {
			return err
		}
	}

	if s.NoModule {
		// The module it is in generates its config's doc comments.
		return goRun(ctx, dir, "generate", "./protos")
	}

	// The first tidy is lenient because protostest does not exist until the protos are generated, and main,
	// which generates the doc comments, can only build once they are.
	for _, args := range [][]string{
		{"mod", "tidy", "-e"},
		{"generate", "./protos"},
		{"generate", "."},
		{"mod", "tidy"},
	} {
		if err = goRun(ctx, dir, args...); err != nil {
			return err
		}
	}
	return nil
}

// files maps each file the capability is scaffolded with, by its path in it, to the template it is written from.
func (s *scaffold) files() map[string]string {
	pkg := s.Package()
	files := map[string]string{
		"main.go":                        "main.go.tmpl",
		"protos/generate.go":             "generate.go.tmpl",
		"protos/" + s.Proto() + ".proto": "capability.proto.tmpl",
		pkg + "/capability.go":           "capability.go.tmpl",
		pkg + "/capability_test.go":      "capability_test.go.tmpl",
	}
	if !s.NoModule {
		files["go.mod"] = "go.mod.tmpl"
		files["project.json"] = "project.json.tmpl"
	}
	return files
}

// generated is what scaffolding writes besides its templates: generated code is marked as such, and these are what
// generating writes whole.
var generated = []string{"go.sum", "protos/" + production.Dir}

// generatedHeader is how generated Go marks itself, https://go.dev/s/generatedcode.
var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// clean removes what scaffolding the capability at dir wrote, from its templates and by generating, keeping any
// other file, so the capability can be scaffolded again over it.
func (s *scaffold) clean(dir string) error {
	// Everything is removed through root, so nothing outside the capability is, even through a symlink in it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	var errs []error
	for path := range s.files() {
		if err := root.Remove(filepath.FromSlash(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, path := range generated {
		errs = append(errs, root.RemoveAll(filepath.FromSlash(path)))
	}
	if err = errors.Join(errs...); err != nil {
		return err
	}

	return fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		b, err := fs.ReadFile(root.FS(), path)
		if err != nil {
			return err
		}
		if generatedHeader.Match(b) {
			return root.Remove(filepath.FromSlash(path))
		}
		return nil
	})
}

// findLibs fills in GoVersion from the libs module the go command resolves from here, returning its directory.
func (s *scaffold) findLibs(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "list", "-m", "-json", libsModule).Output()
	if err != nil {
		return "", fmt.Errorf("failed to find %s, run this from a module that requires it: %w", libsModule, err)
	}
	var libs struct{ Dir, GoVersion string }
	if err = json.Unmarshal(out, &libs); err != nil {
		return "", err
	}

	s.GoVersion = libs.GoVersion
	return libs.Dir, nil
}

// place fills in Module and Libs for a module at dir.
func (s *scaffold) place(dir, libsDir string) (err error) {
	if s.Libs, err = filepath.Rel(dir, libsDir); err != nil {
		return err
	}

	// The repo is libs' parent; a module outside it is placed as if it were at <category>/<name>.
	s.Module = repoModule + "/" + s.Category + "/" + s.Name
	if rel, err := filepath.Rel(filepath.Dir(libsDir), dir); err == nil && filepath.IsLocal(rel) {
		s.Module = repoModule + "/" + filepath.ToSlash(rel)
	}
	return nil
}

// write executes the template name into path, formatting it if it is Go.
func (s *scaffold) write(path, name string) error {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, s); err != nil {
		return err
	}
	out := []byte(b.String())
	if filepath.Ext(path) == ".go" {
		var err error
		if out, err = format.Source(out); err != nil {
			return fmt.Errorf("failed to format %s: %w", path, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644) //nolint:gosec // Source, readable like any other.
}

// goRun runs go in dir, letting it update go.mod and go.sum with what the generators need.
func goRun(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go %s failed: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
