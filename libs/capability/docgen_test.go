package capability

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Run anywhere but the capability's own module, docgen would find none of its configs to write, so it refuses.
// Writing is covered by internal/example, whose doc comments CI regenerates with it.
func TestDocgenRefusesAnotherModule(t *testing.T) {
	t.Chdir(t.TempDir())

	require.ErrorContains(t, execute(t, logger.Test(t), fakeConstructor, "docgen"), "docgen must run in")
}
