package capability

import (
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/x/config/commentparsing"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

// docsCommand generates the DocComments every config bound on the binary needs for its flags' help
// text, so a capability's own config is documented by running its binary rather than by a
// generator of its own:
//
//	//go:generate go run . docs
//
// It writes a doccomments_gen.go beside each config struct - registered with ProvideConfig, by this
// package or any dependency - declared in the module enclosing the working directory. A struct from another module - this package's sections, when a capability
// binary runs it - is read from the DocComments that module already compiled in, so this package
// generates its own too (see internal/docgen).
//
// The binder decodes before any command runs, docs included, so a config with a required field
// has to be satisfied to generate its docs.
//
// It documents what both commands register: each registers configs the other does not -
// capabilities differs between them, say.
func docsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "docs",
		Short: "Generate the doc comments the config's help text is read from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var roots []any
			for _, r := range []*registrations{runRegistrations, embedRegistrations} {
				for _, namespace := range slices.Sorted(maps.Keys(r.configs)) {
					roots = append(roots, r.configs[namespace].value)
				}
			}

			return commentparsing.Run(commentparsing.RunArgs{
				Roots:  roots,
				Markup: tomlmarkup.New(),
				// Per binary: a run removes files its Tool wrote and did not produce this time, so
				// two binaries in one module sharing a name would delete each other's.
				Tool:        cmd.Root().Name() + " docs",
				LocalPrefix: "github.com/smartcontractkit",
			})
		},
	}
}
