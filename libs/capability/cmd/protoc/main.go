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
	"path/filepath"

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

func generate(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "go", "list", "-json", ".").Output()
	if err != nil {
		return fmt.Errorf("failed to find the package: %w", err)
	}
	var pkg struct {
		Dir, ImportPath string
		Module          struct{ Dir string }
	}
	if err = json.Unmarshal(out, &pkg); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(pkg.Dir, "*.proto"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s has no .proto files", pkg.Dir)
	}
	for i, file := range files {
		files[i] = filepath.Base(file)
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
