package protofetch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Options are what Generate generates and how.
type Options struct {
	// Module is the directory of the module generated into. What is fetched is what the protofetch.toml at or above
	// it fetches, see Root.
	Module string
	// Modules are the directories of other modules whose go.mod requires the Go generated from what is fetched,
	// which Generate keeps in step with it, after Module's.
	Modules []string
	// Dir is the directory protoc runs in, and generates into.
	Dir string
	// Files are the protos to generate, relative to Dir or to what is fetched.
	Files []string
	// Fetched is a directory in what is fetched whose protos are generated too, once they are fetched, into Dir rather
	// than at their path in what is fetched.
	Fetched string
	// GoPackage is the Go package Files are generated to.
	GoPackage string
	// Plugins are the protoc plugins to generate with, each a package go build builds in Dir, so it is the version
	// the Go generated there is built with.
	Plugins []string
	// Binaries are protoc plugins to generate with that are already built, by their paths.
	Binaries []string
	// Includes are directories to import from besides Dir and what is fetched.
	Includes []string
	// GoPackages are the Go packages of protos imported from Includes, by their paths in them.
	GoPackages map[string]string
}

// Generate generates Files with Plugins, importing from what the module fetches with protofetch. It first syncs what
// is fetched with the modules' go.mod files, so what it generates from is what the Go it links was generated from,
// then fetches.
func Generate(ctx context.Context, opts Options) error {
	root, err := Root(opts.Module)
	if err != nil {
		return err
	}

	fetched, err := OutDir(root)
	if err != nil {
		return err
	}

	tc := commands{protofetch: binary(root)}
	if err = sync(ctx, root, tc, append([]string{opts.Module}, opts.Modules...)...); err != nil {
		return err
	}

	fetch := exec.CommandContext(ctx, tc.protofetch, "fetch", "--locked") //nolint:gosec // The repo's protofetch.
	fetch.Dir = root
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("protofetch fetch failed, install it with pnpm install: %w\n%s", err, out)
	}

	files := slices.Clone(opts.Files)
	if opts.Fetched != "" {
		found, err := fs.Glob(os.DirFS(fetched), path.Join(opts.Fetched, "*.proto"))
		if err != nil {
			return err
		}

		if len(found) == 0 {
			return fmt.Errorf("%s fetches no protos in %s", opts.Module, opts.Fetched)
		}

		files = append(files, found...)
	}

	tools, err := os.MkdirTemp("", "protoc-plugins-")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tools)

	mappings := maps.Clone(goPackages)
	maps.Copy(mappings, opts.GoPackages)
	for _, file := range files {
		mappings[file] = opts.GoPackage
	}

	args := []string{"-I", ".", "-I", fetched}
	for _, include := range opts.Includes {
		args = append(args, "-I", include)
	}

	binaries := slices.Clone(opts.Binaries)
	for _, plugin := range opts.Plugins {
		binary := filepath.Join(tools, path.Base(plugin))
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, plugin) //nolint:gosec // A plugin the caller names.
		build.Dir = opts.Dir
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to build %s: %w\n%s", plugin, err, out)
		}

		binaries = append(binaries, binary)
	}

	for _, binary := range binaries {
		name := strings.TrimPrefix(filepath.Base(binary), "protoc-gen-")
		args = append(args, "--plugin=protoc-gen-"+name+"="+binary, "--"+name+"_out=.", "--"+name+"_opt=paths=source_relative")
		for file, goPkg := range mappings {
			args = append(args, "--"+name+"_opt=M"+file+"="+goPkg)
		}
	}

	// One run for every file, as production protos need: production in one file can need another's messages.
	cmd := exec.CommandContext(ctx, "protoc", append(args, files...)...) //nolint:gosec // Arguments built from opts.
	cmd.Dir = opts.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("protoc failed: %w\n%s", err, out)
	}

	if opts.Fetched == "" {
		return nil
	}

	return flatten(opts.Dir, opts.Fetched)
}

// flatten moves what was generated from the fetched protos in sub, which plugins write at their path, to dir, which
// is GoPackage, then removes the directories that emptied.
func flatten(dir, sub string) error {
	from := filepath.Join(dir, filepath.FromSlash(sub))
	entries, err := os.ReadDir(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	for _, e := range entries {
		if err = os.Rename(filepath.Join(from, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}

	// Only the directories it emptied, so nothing else in dir goes with them.
	for d := from; d != filepath.Clean(dir); d = filepath.Dir(d) {
		left, err := os.ReadDir(d)
		if err != nil || len(left) > 0 {
			return err
		}

		if err = os.Remove(d); err != nil {
			return err
		}
	}

	return nil
}
