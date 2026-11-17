package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

// psbMigration is the feature of what a capability released before it moved to its own protos, so it may be released
// without first being in progress.
const psbMigration = "psb_migration"

type featureLists struct {
	released   map[string]bool
	inProgress map[string]bool
}

// readFeatures returns the feature lists of the development protos of every capability in the checkout at root, by
// development package, so a capability that moves is still compared with itself.
func readFeatures(root string) (map[string]featureLists, error) {
	features := map[string]featureLists{}
	err := walkProduction(root, func(path string) error {
		files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.proto"))
		if err != nil {
			return err
		}

		for _, file := range files {
			fd, err := parseUnresolved(file)
			if err != nil {
				return err
			}

			lists, ok := features[fd.GetPackage()]
			if !ok {
				lists = featureLists{released: map[string]bool{}, inProgress: map[string]bool{}}
				features[fd.GetPackage()] = lists
			}

			addLists(lists, fd.GetOptions().GetUninterpretedOption())
		}

		return nil
	})

	return features, err
}

func addLists(lists featureLists, opts []*descriptorpb.UninterpretedOption) {
	released := string(feature.E_Released.TypeDescriptor().FullName())
	inProgress := string(feature.E_InProgress.TypeDescriptor().FullName())
	for _, opt := range opts {
		name := opt.GetName()
		if len(name) != 1 || !name[0].GetIsExtension() {
			continue
		}

		switch strings.TrimPrefix(name[0].GetNamePart(), ".") {
		case released:
			lists.released[string(opt.GetStringValue())] = true
		case inProgress:
			lists.inProgress[string(opt.GetStringValue())] = true
		}
	}
}

func parseUnresolved(file string) (*descriptorpb.FileDescriptorProto, error) {
	f, err := os.Open(file) //nolint:gosec // A capability's proto in the checkout.
	if err != nil {
		return nil, err
	}

	defer f.Close()

	handler := reporter.NewHandler(nil)
	ast, err := parser.Parse(file, f, handler)
	if err != nil {
		return nil, err
	}

	res, err := parser.ResultFromAST(ast, false, handler)
	if err != nil {
		return nil, err
	}

	return res.FileDescriptorProto(), nil
}

// checkReleases checks every feature head releases was in progress, or already released, in base, so features are
// only released once they've been developed.
func checkReleases(base, head map[string]featureLists) error {
	var errs []error
	for _, pkg := range slices.Sorted(maps.Keys(head)) {
		was := base[pkg]
		for _, name := range slices.Sorted(maps.Keys(head[pkg].released)) {
			if name != psbMigration && !was.released[name] && !was.inProgress[name] {
				errs = append(errs, fmt.Errorf("%s releases feature %q, which isn't in progress on the baseline: list it as "+
					"in progress first, as only %q can be released directly", pkg, name, psbMigration))
			}
		}
	}

	return errors.Join(errs...)
}
