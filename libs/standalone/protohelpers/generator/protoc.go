package generator

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// protoc is a configured protoc invocation.
type protoc struct {
	// Plugins are the code generators to run, each over every file, with the
	// same includes and the same links.
	Plugins []Plugin

	// Includes are the directories protoc resolves imports against, in order.
	Includes []string

	// Links are the Go import path each .proto generates into, keyed by the
	// .proto's path as written in an import statement. A .proto carrying no
	// `option go_package` - the convention in the CRE protos - has to be linked
	// before it can be generated or imported.
	Links map[string]string

	// Tools is the directory the plugin binaries were installed to.
	Tools string
}

// generate writes the code for files, which are paths relative to one of the
// include directories, under out. Generation is source-relative: a package's Go
// import path is decided by Links, not by where protoc puts the file.
func (p *protoc) generate(out string, files []string) error {
	var args []string
	for _, include := range p.Includes {
		args = append(args, "-I", include)
	}

	// Sorted so that a run's arguments, and so its output, do not depend on map
	// iteration order.
	linked := make([]string, 0, len(p.Links))
	for file := range p.Links {
		linked = append(linked, file)
	}
	sort.Strings(linked)

	for _, plugin := range p.Plugins {
		args = append(args,
			fmt.Sprintf("--plugin=protoc-gen-%s=%s", plugin.Name, plugin.binary(p.Tools)),
			fmt.Sprintf("--%s_out=%s", plugin.Name, out),
			fmt.Sprintf("--%s_opt=paths=source_relative", plugin.Name),
		)

		for _, file := range linked {
			args = append(args, fmt.Sprintf("--%s_opt=M%s=%s", plugin.Name, file, p.Links[file]))
		}
	}

	cmd := exec.Command("protoc", append(args, files...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", cmd.String(), err, output)
	}

	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		fmt.Println(trimmed)
	}

	return nil
}
