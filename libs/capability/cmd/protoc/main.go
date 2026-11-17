// Command protoc generates the Go messages and capability servers for the .proto files of the package it is run
// in, a capability's protos package, into it, and their production protos into its production directory, for the
// SDKs to generate from:
//
//	//go:generate go run github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc
//
// The protos they import are fetched first, with protofetch, at the commits in the protofetch.lock at the root of the
// repo, which is first moved to the commits the module's go.mod requires the Go of, or go.mod to the lock's, whichever
// is newer. The plugins are built from the versions the calling module requires.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc-gen-cre/production"
	"github.com/smartcontractkit/capabilities/libs/capability/feature"
	"github.com/smartcontractkit/capabilities/libs/capability/protofetch"
)

// featurePkg is the Go package feature.proto is generated to.
const featurePkg = "github.com/smartcontractkit/capabilities/libs/capability/feature"

var plugins = []string{
	"google.golang.org/protobuf/cmd/protoc-gen-go",
	"github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc-gen-cre",
}

func main() {
	if err := generate(context.Background()); err != nil {
		log.Fatal(err)
	}
}

type goPackage struct {
	Dir        string
	ImportPath string
	Module     struct{ Dir string }
}

func generate(ctx context.Context) error {
	pkg, files, err := protos(ctx)
	if err != nil {
		return err
	}

	tools, err := os.MkdirTemp("", "protoc-include-")
	if err != nil {
		return err
	}

	defer os.RemoveAll(tools)

	featureProto := filepath.Join(tools, filepath.FromSlash(feature.Path))
	if err = os.MkdirAll(filepath.Dir(featureProto), 0o755); err != nil {
		return err
	}

	if err = os.WriteFile(featureProto, feature.Proto, 0o600); err != nil {
		return err
	}

	if files, err = stage(pkg.Dir, files, tools); err != nil {
		return err
	}

	// Every file has a production file, so ones left from files since removed would be stale.
	if err = os.RemoveAll(filepath.Join(pkg.Dir, production.Dir)); err != nil {
		return err
	}

	return protofetch.Generate(ctx, protofetch.Options{
		Module:     pkg.Module.Dir,
		Dir:        pkg.Dir,
		Files:      files,
		GoPackage:  pkg.ImportPath,
		Plugins:    plugins,
		Includes:   []string{tools},
		GoPackages: map[string]string{feature.Path: featurePkg},
	})
}

var packageLine = regexp.MustCompile(`(?m)^package\s+([\w.]+)\s*;`)

// stage copies files from dir into includes at their proto package's path, which they are generated, and registered,
// by: their names alone could be another capability's, which a binary linking both would register twice. It returns
// their paths in includes.
func stage(dir string, files []string, includes string) ([]string, error) {
	root, err := os.OpenRoot(includes)
	if err != nil {
		return nil, err
	}

	defer root.Close()

	staged := make([]string, len(files))
	pkg := ""
	for i, file := range files {
		b, err := os.ReadFile(filepath.Join(dir, file)) //nolint:gosec // A proto of the package generated.
		if err != nil {
			return nil, err
		}

		m := packageLine.FindSubmatch(b)
		if m == nil {
			return nil, fmt.Errorf("%s has no package", file)
		}

		if pkg != "" && pkg != string(m[1]) {
			return nil, fmt.Errorf("%s is in package %s, not %s: a Go package's protos are one proto package", file, m[1], pkg)
		}

		pkg = string(m[1])
		staged[i] = path.Join(strings.ReplaceAll(pkg, ".", "/"), file)
		if err = root.MkdirAll(path.Dir(staged[i]), 0o700); err != nil {
			return nil, err
		}

		if err = root.WriteFile(staged[i], b, 0o600); err != nil {
			return nil, err
		}
	}

	return staged, nil
}

// protos returns the package in the current directory, and the names of its .proto files.
func protos(ctx context.Context) (goPackage, []string, error) {
	var pkg goPackage
	out, err := exec.CommandContext(ctx, "go", "list", "-json", ".").Output()
	if err != nil {
		return pkg, nil, fmt.Errorf("failed to find the package: %w", err)
	}

	if err = json.Unmarshal(out, &pkg); err != nil {
		return pkg, nil, err
	}

	files, err := filepath.Glob(filepath.Join(pkg.Dir, "*.proto"))
	if err != nil {
		return pkg, nil, err
	}

	if len(files) == 0 {
		return pkg, nil, fmt.Errorf("%s has no .proto files", pkg.Dir)
	}

	for i, file := range files {
		files[i] = filepath.Base(file)
	}

	return pkg, files, nil
}
