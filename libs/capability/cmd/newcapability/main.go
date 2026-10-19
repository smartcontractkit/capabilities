// Command newcapability scaffolds a capability module - its protos, main, go.mod, config and a test - then
// generates and tidies it. The module is created at <out-dir>/<category>/<name>, where out-dir defaults to the root
// of the repo libs is in; the category and name it is not given are prompted for:
//
//	go run ./capability/cmd/newcapability --trigger --category scheduler --name cron
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/x/config/cli"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

func main() {
	cmd, err := newCommand()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newCommand() (*cobra.Command, error) {
	var s scaffold
	cmd := &cobra.Command{
		Use:   "newcapability",
		Short: "Scaffold a new capability module",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := bufio.NewReader(cmd.InOrStdin())
			if err := orPrompt(cmd, in, &s.Category, "Category (e.g. scheduler)"); err != nil {
				return err
			}
			if err := orPrompt(cmd, in, &s.Name, "Capability (e.g. cron)"); err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return s.run(cmd.Context())
		},
	}

	b, err := cli.New(cli.Options{Markup: tomlmarkup.New(), Prefixes: []string{"NEWCAPABILITY"}})
	if err != nil {
		return nil, err
	}
	return cmd, b.Register(cmd, &s.Options)
}

// orPrompt sets *v from a line read from in, after prompting for label, when it was not given.
func orPrompt(cmd *cobra.Command, in *bufio.Reader, v *string, label string) error {
	if *v != "" {
		return nil
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: ", label)
	line, err := in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return fmt.Errorf("failed to read %s: %w", label, err)
	}
	*v = strings.TrimSpace(line)
	return nil
}
