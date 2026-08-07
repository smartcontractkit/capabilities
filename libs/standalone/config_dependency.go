package standalone

import (
	"context"
	"os"

	"github.com/spf13/cobra"
)

type configDependency[T any] struct {
	parse func([]byte) (T, error)
	file  string
}

func NewConfigDependency[T any](parse func([]byte) (T, error)) BootstrapDependency[T] {
	return OnceBootstrapper[T](&configDependency[T]{parse: parse})
}

func (c *configDependency[T]) Get(_ context.Context, _ CommonConfig) (T, error) {
	content, err := os.ReadFile(c.file)
	if err != nil {
		var t T
		return t, err
	}

	return c.parse(content)
}

// Namespace is empty: --config-file is a process-wide setting, not one dependency's.
func (c *configDependency[T]) Namespace() string { return "" }

func (c *configDependency[T]) AddCommands(command *cobra.Command) {
	command.PersistentFlags().StringVar(&c.file, "config-file", "", "specifies the config file to load from")
}
