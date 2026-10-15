package capability

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

const emptyPluginName = "empty"

func emptyHandshakeConfig() plugin.HandshakeConfig {
	return plugin.HandshakeConfig{
		MagicCookieKey:   "CL_PLUGIN_EMPTY_MAGIC_COOKIE",
		MagicCookieValue: "2543d46d-e317-4bc4-a6be-1c018bf3a8c7",
	}
}

// emptyLoop exposes no gRPC services. Serving it only satisfies the go-plugin host's process
// supervision; the capability itself is reached through the registry, not through the plugin.
type emptyLoop struct {
	plugin.NetRPCUnsupportedPlugin
}

func (*emptyLoop) GRPCServer(*plugin.GRPCBroker, *grpc.Server) error {
	return nil
}

func (*emptyLoop) GRPCClient(context.Context, *plugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return struct{}{}, nil
}
