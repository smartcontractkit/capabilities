package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Plugin is a protoc code generator: protoc runs it as protoc-gen-<Name>, and
// drives it with --<Name>_out and --<Name>_opt.
type Plugin struct {
	Name string

	// Package is the plugin's main package. The module holding it, and so the
	// version of the plugin that runs, is whatever the capability being
	// generated resolves that package to: the plugin is built from the commit
	// the capability's go.mod pins, the same one its generator came from.
	//
	// That is what lets this generator change without breaking capabilities
	// that have not moved yet. A capability pinning an older version keeps
	// generating with the plugin from that version, so a plugin gaining an
	// option, or emitting a new file, reaches a capability only when it updates
	// - and updating the plugin and the library it generates against is one
	// change, not two that can disagree.
	Package string
}

// GoPlugin generates the Go structs, via protoc-gen-go.
var GoPlugin = Plugin{
	Name:    "go",
	Package: "google.golang.org/protobuf/cmd/protoc-gen-go",
}

// CREPlugin generates the server for each capability service in a proto.
var CREPlugin = Plugin{
	Name:    "cre",
	Package: "github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/protoc",
}

// install builds the plugin's binary into dir, from the source the module in
// from resolves its package to.
func (p Plugin) install(dir, from string) error {
	list := exec.Command("go", "list", "-f", "{{.Dir}}", "--", p.Package)
	list.Dir = from
	list.Stderr = os.Stderr

	out, err := list.Output()
	if err != nil {
		return fmt.Errorf("%q is not in the build list, run `go get %s`: %w", p.Package, p.Package, err)
	}

	pkg := strings.TrimSpace(string(out))
	if pkg == "" {
		return fmt.Errorf("%q is not downloaded, run `go mod download`", p.Package)
	}

	tools, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	// Named rather than left to go build, which would name the binary after the
	// package's directory - right for cmd/protoc-gen-go, wrong for a plugin
	// whose directory is not already called protoc-gen-<name>.
	build := exec.Command("go", "build", "-o", p.binary(tools), ".")
	build.Dir = pkg

	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		return fmt.Errorf("%s in %s: %w\n%s", build.String(), pkg, buildErr, output)
	}

	return nil
}

// binary is the path install built the plugin to.
func (p Plugin) binary(dir string) string {
	return filepath.Join(dir, "protoc-gen-"+p.Name)
}
