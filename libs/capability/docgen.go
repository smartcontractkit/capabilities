package capability

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/x/config/commentparsing"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

// newDocgenCmd generates the DocComments the flags' help text is read from, for every config the capability
// provides, so a capability's main can:
//
//	//go:generate go run . docgen
//
// commentparsing parses the configs declared in the module it runs in, and reads the rest - libs' own, say -
// from what their modules generated, so only this module's are written. Run from any other module, it would
// find none of the capability's configs its own and write nothing, so that is an error.
func newDocgenCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "docgen",
		Short:        "Generate the doc comments of the capability's configs, for go:generate",
		Args:         cobra.NoArgs,
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, ok := debug.ReadBuildInfo()
			if !ok {
				return errors.New("no build info to find the capability's module by")
			}
			out, err := exec.CommandContext(cmd.Context(), "go", "list", "-m").Output()
			if err != nil {
				return fmt.Errorf("failed to find the module docgen runs in: %w", err)
			}
			if mod := strings.TrimSpace(string(out)); mod != info.Main.Path {
				return fmt.Errorf("docgen must run in %s, the capability's module, not %s", info.Main.Path, mod)
			}
			return commentparsing.Run(commentparsing.RunArgs{
				Markup:      tomlmarkup.New(),
				Roots:       append(RunInjector.Configs(), EmbedInjector.Configs()...),
				Tool:        info.Path + " docgen",
				LocalPrefix: "github.com/smartcontractkit",
			})
		},
	}
}
