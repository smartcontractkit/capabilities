// Command docgen is a capability binary run only for its docs command: it generates the
// DocComments for this package's own config sections, which every capability binary reads its
// flags' help text from.
package main

import (
	"context"
	"errors"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/libs/capability"
)

func main() {
	capability.Run(context.Background(), func(do.Injector) (capability.Capability, error) {
		return nil, errors.New("docgen hosts nothing: run its docs command")
	}, capability.NewConfigProvider(struct{}{}))
}
