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
	"slices"
	"strings"
	"text/template"

	"github.com/smartcontractkit/capabilities/libs/capability/cmd/newcapability/options"
)

const (
	repoModule = "github.com/smartcontractkit/capabilities"
	libsModule = repoModule + "/libs"
)

// templates are at the paths scaffolding writes them to, with .tmpl added.
//
//go:embed templates
var templates embed.FS

// moduleFiles are what only a capability with a module of its own has.
var moduleFiles = []string{"go.mod", "project.json"}

// identifier is what a category or name must be, as they are used in module paths and proto packages.
var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

type scaffold struct {
	options.Options
	Module    string
	GoVersion string
}

func (s *scaffold) Project() string {
	return strings.ReplaceAll(strings.TrimPrefix(s.Module, repoModule+"/"), "/", "-")
}

func (s *scaffold) run(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}

	libsDir, err := s.findLibs(ctx)
	if err != nil {
		return err
	}

	if s.OutDir == "" {
		s.OutDir = filepath.Dir(libsDir)
	}

	dir, err := s.prepare(filepath.Join(s.OutDir, s.Category, s.Name))
	if err != nil {
		return err
	}

	if err = s.writeFiles(dir, libsDir); err != nil {
		return err
	}

	return s.generate(ctx, dir)
}

func (s *scaffold) validate() error {
	for _, v := range []string{s.Category, s.Name} {
		if !identifier.MatchString(v) {
			// It is part of the module path, the proto package and the capability ID: no case to clash on
			// case-insensitive filesystems or be escaped in the module path, and no separators protobuf rejects.
			return fmt.Errorf("%q must be lowercase letters and digits, starting with a letter", v)
		}
	}

	return nil
}

// prepare makes dir, cleaning what an earlier scaffold wrote there if --force allows it, and returns it resolved, so
// the replace is relative to where the module really is, not a symlink to it.
func (s *scaffold) prepare(dir string) (string, error) {
	if _, err := os.Stat(dir); err == nil {
		if !s.Force {
			return "", fmt.Errorf("%s already exists, use --force to replace it", dir)
		}

		if err = s.clean(dir); err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	return filepath.EvalSymlinks(dir)
}

func (s *scaffold) writeFiles(dir, libsDir string) error {
	s.place(dir, libsDir)
	files, err := s.files()
	if err != nil {
		return err
	}

	for _, file := range files {
		if err = s.write(dir, file); err != nil {
			return err
		}
	}

	return nil
}

func (s *scaffold) generate(ctx context.Context, dir string) error {
	if s.NoModule {
		// The module it is in generates its config's doc comments.
		return goRun(ctx, dir, "generate", "./protos")
	}

	// The first tidy is lenient because protostest does not exist until the protos are generated, and main,
	// which generates the doc comments, can only build once they are.
	for _, args := range [][]string{
		{"get", libsModule + "@main"},
		{"mod", "tidy", "-e"},
		{"generate", "./protos"},
		{"generate", "."},
		{"mod", "tidy"},
	} {
		if err := goRun(ctx, dir, args...); err != nil {
			return err
		}
	}

	return nil
}

// files returns the paths, relative to the capability, of what scaffolding writes from its templates.
func (s *scaffold) files() ([]string, error) {
	var files []string
	err := fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		file := strings.TrimSuffix(strings.TrimPrefix(path, "templates/"), ".tmpl")
		if !s.NoModule || !slices.Contains(moduleFiles, file) {
			files = append(files, file)
		}

		return nil
	})

	return files, err
}

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

	files, err := s.files()
	if err != nil {
		return err
	}

	var errs []error
	for _, path := range files {
		if err := root.Remove(filepath.FromSlash(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
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

func (s *scaffold) place(dir, libsDir string) {
	s.Module = repoModule + "/" + s.Category + "/" + s.Name
	if rel, err := filepath.Rel(filepath.Dir(libsDir), dir); err == nil && filepath.IsLocal(rel) {
		s.Module = repoModule + "/" + filepath.ToSlash(rel)
	}
}

func (s *scaffold) write(dir, file string) error {
	t, err := template.ParseFS(templates, "templates/"+file+".tmpl")
	if err != nil {
		return err
	}

	var b strings.Builder
	if err = t.Execute(&b, s); err != nil {
		return err
	}

	path := filepath.Join(dir, filepath.FromSlash(file))
	out := []byte(b.String())
	if filepath.Ext(path) == ".go" {
		if out, err = format.Source(out); err != nil {
			return fmt.Errorf("failed to format %s: %w", path, err)
		}
	}

	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o600)
}

func goRun(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go %s failed: %w\n%s", strings.Join(args, " "), err, out)
	}

	return nil
}
