package generator

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// protoPackage captures the name in a .proto's package declaration. Anything
// after a "//" is dropped first, so a commented-out declaration cannot match.
var protoPackage = regexp.MustCompile(`(?m)^[^/\n]*\bpackage\s+([A-Za-z0-9_.]+)\s*;`)

// stage copies each file into dir at the path its proto package declares, and
// returns those paths, in the order the files were given.
//
// The path a .proto is compiled under is baked into the generated descriptor,
// and is what other protos import it by, so it has to follow the proto package
// - package capabilities.internal.consensus.v1alpha compiles as
// capabilities/internal/consensus/v1alpha/consensus.proto - however the file is
// laid out on disk. Keeping a capability's .proto next to the capability is a
// layout choice; the namespace it generates into is not, and staging a copy is
// what keeps the two independent.
func stage(dir string, files []string) ([]string, error) {
	staged := make([]string, 0, len(files))
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}

		match := protoPackage.FindSubmatch(content)
		if match == nil {
			return nil, fmt.Errorf("%s declares no proto package", file)
		}

		name := path.Join(strings.ReplaceAll(string(match[1]), ".", "/"), filepath.Base(file))
		dst := filepath.Join(dir, filepath.FromSlash(name))

		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return nil, err
		}

		if err = os.WriteFile(dst, content, 0600); err != nil {
			return nil, err
		}

		staged = append(staged, name)
	}

	return staged, nil
}
