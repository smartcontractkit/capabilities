package capability

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

// The empty LOOP: the server side of a go-plugin plugin that exposes no services, so a node can
// launch this binary and supervise its liveness through go-plugin while its real work is served
// elsewhere. The handshake has to match the node's, which still ships it as loop.EmptyLoop.
const emptyPluginName = "empty"

func emptyHandshakeConfig() plugin.HandshakeConfig {
	return plugin.HandshakeConfig{
		MagicCookieKey:   "CL_PLUGIN_EMPTY_MAGIC_COOKIE",
		MagicCookieValue: "b9a1e7c4-3f2d-4a6b-8c1e-5d9f0a2b3c4d",
	}
}

type emptyLoop struct {
	plugin.NetRPCUnsupportedPlugin
}

func (*emptyLoop) GRPCServer(*plugin.GRPCBroker, *grpc.Server) error { return nil }

func (*emptyLoop) GRPCClient(context.Context, *plugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return struct{}{}, nil
}
