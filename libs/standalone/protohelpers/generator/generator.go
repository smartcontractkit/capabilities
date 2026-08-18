// Package generator generates the Go code for a capability's protos. It is run
// from the root of the capability's module, and generates every .proto in that
// module's protos directory into the Go package of the same name.
//
// It replaces the generator in chainlink-protos' installer, which compiles
// against a copy of the CRE protos embedded in that module: the copy is only as
// current as the release it was embedded in, and it cannot see a proto that
// lives anywhere else. This one takes the .proto sources of whatever version of
// chainlink-protos the capability already depends on, so the schemas and the
// generated Go code cannot drift apart, and it generates a capability's own
// .proto from where the capability keeps it - beside the capability, in this
// repository - rather than requiring it to be checked in to chainlink-protos
// first.
package generator

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
)

// Protos is where a capability keeps its .proto files, and therefore also the
// Go package they generate into. It is a convention rather than a setting: a
// capability that imports another finds its protos by it, without needing to
// know anything about that capability beyond its module path.
const Protos = "protos"

// creModule holds the protos every capability is written against: the values
// its inputs and outputs are made of, the SDK types a capability's methods take
// and return, and the annotations that mark a service as a capability.
const creModule = "github.com/smartcontractkit/chainlink-protos/cre/go"

// creSources are the directories of creModule's repository to vendor. They sit
// above the Go module's root, which is why they never reach the module cache
// and have to be vendored at all.
var creSources = []string{"../values", "../sdk", "../tools"}

// creLinks is the Go package each vendored CRE proto is already generated into.
// Generating them again here would give a capability its own copies of types the
// CRE runtime hands it, which would not be the same Go types.
var creLinks = map[string]string{
	"values/v1/values.proto":                     creModule + "/values/pb",
	"sdk/v1alpha/sdk.proto":                      creModule + "/sdk",
	"tools/generator/v1alpha/cre_metadata.proto": creModule + "/tools/generator",
}

// Generator generates a capability's protos with a chosen set of protoc
// plugins.
type Generator struct {
	// Plugins are the protoc code generators to run over every .proto, each one
	// building on the same includes and the same package links.
	Plugins []Plugin
}

// Generate runs what a capability needs: the messages, and the server for each
// service. A capability wanting more than that - a client, a mock - builds a
// Generator with those plugins instead.
func Generate(capabilities ...string) error {
	return (&Generator{Plugins: []Plugin{GoPlugin, CREPlugin}}).Generate(capabilities...)
}

// Generate generates the protos of the module the working directory is in.
// Everything is resolved from that module's root, so it does not matter where
// under the module it is run from - a go:generate directive can sit beside the
// main that calls this rather than at the module root.
//
// capabilities are the module paths of other capabilities whose protos these
// ones import. Each is resolved against this module's go.mod, its protos are
// compiled alongside, and they are linked to the Go package that capability
// already generated them into - which is its module path plus protos, by the
// same convention this generator writes.
func (g *Generator) Generate(capabilities ...string) error {
	if len(g.Plugins) == 0 {
		return fmt.Errorf("no protoc plugins configured")
	}

	mod, err := resolve(".", "")
	if err != nil {
		return err
	}

	dir := filepath.Join(mod.Dir, Protos)

	files, err := filepath.Glob(filepath.Join(dir, "*.proto"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .proto files in %s", dir)
	}

	// Everything protoc compiles is read from one staging directory, so that a
	// .proto is compiled under the path its own proto package declares, wherever
	// its source is kept. See stage.
	staging, err := os.MkdirTemp("", "protos-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	run := &protoc{Plugins: g.Plugins, Includes: []string{staging}, Links: map[string]string{}}

	staged, err := stage(staging, files)
	if err != nil {
		return err
	}
	for _, file := range staged {
		run.Links[file] = path.Join(mod.Path, Protos)
	}

	items := map[string][]string{creModule: creSources}
	for _, capability := range capabilities {
		items[capability] = []string{Protos}
	}

	vendored, err := (&downloader{Items: items, Dir: mod.Dir, Vendor: repoRoot(mod.Dir)}).download()
	if err != nil {
		return err
	}

	for _, v := range vendored {
		// The CRE protos are laid out by their proto package already, so they
		// are compiled where they were vendored rather than staged.
		if v.Module == creModule {
			run.Includes = append(run.Includes, v.Include)
			continue
		}

		if err = link(run, staging, v.Dir, path.Join(v.Module, Protos)); err != nil {
			return fmt.Errorf("%s: %w", v.Module, err)
		}
	}

	run.Includes = dedupe(run.Includes)
	maps.Copy(run.Links, creLinks)

	// Every plugin binary is built from the version this module depends on, so
	// that regenerating cannot pick up whatever happens to be on PATH.
	tools, err := os.MkdirTemp("", "protoc-plugins-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tools)

	run.Tools = tools
	for _, plugin := range run.Plugins {
		if err = plugin.install(tools, mod.Dir); err != nil {
			return fmt.Errorf("installing protoc-gen-%s: %w", plugin.Name, err)
		}
	}

	// Generated files land under their .proto's path, which is a proto
	// namespace rather than anything belonging in a Go module, so they are
	// generated aside and then moved into the protos package.
	out, err := os.MkdirTemp("", "protos-go-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(out)

	if err = run.generate(out, staged); err != nil {
		return err
	}

	return move(out, dir)
}

// repoRoot returns the root of the repository dir is in, or dir itself if it is
// not in one.
//
// The vendored .proto sources are keyed by module and version, so every module
// of every capability in a repository can be served out of one cache rather
// than fetching the same chainlink-protos release once per module.
func repoRoot(dir string) string {
	for at := dir; ; {
		// A worktree's .git is a file rather than a directory, so this only
		// asks whether it exists.
		if _, err := os.Stat(filepath.Join(at, ".git")); err == nil {
			return at
		}

		parent := filepath.Dir(at)
		if parent == at {
			return dir
		}
		at = parent
	}
}

// link stages the protos of another capability this one imports, so that they
// can be imported by the path their proto package declares, and links them to
// the Go package that capability generated them into.
func link(run *protoc, staging, dir, goPkg string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.proto"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no .proto files in %s", dir)
	}

	staged, err := stage(staging, files)
	if err != nil {
		return err
	}

	for _, file := range staged {
		run.Links[file] = goPkg
	}

	return nil
}

// move flattens the generated tree into the protos package: a Go package is one
// directory, whatever nesting the proto namespace has.
//
// Everything generated is moved, rather than the files a plugin is expected to
// have written, since each plugin names its output as it likes. A plugin that
// generates into subdirectories of its own - a package of mocks, say - will
// need more than a flattening here.
func move(from, to string) error {
	return filepath.WalkDir(from, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		dst := filepath.Join(to, entry.Name())
		if err = os.Rename(p, dst); err != nil {
			return err
		}

		fmt.Println("Generated", dst)
		return nil
	})
}
