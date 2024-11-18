## Capabilities modules and org dependencies
```mermaid
flowchart LR

	capabilities/cron --> capabilities/libs/loopserver
	click capabilities/cron href "https://github.com/smartcontractkit/capabilities"
	capabilities/devenv --> chainlink/v2
	click capabilities/devenv href "https://github.com/smartcontractkit/capabilities"
	capabilities/integration_tests --> chainlink/v2
	click capabilities/integration_tests href "https://github.com/smartcontractkit/capabilities"
	capabilities/kvstore --> capabilities/libs/loopserver
	capabilities/kvstore --> capabilities/libs/testutils
	click capabilities/kvstore href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs/cll --> libocr
	click capabilities/libs/cll href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs/loopserver --> chainlink-common
	click capabilities/libs/loopserver href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs/testutils --> chainlink-common
	click capabilities/libs/testutils href "https://github.com/smartcontractkit/capabilities"
	capabilities/readcontract --> capabilities/libs/loopserver
	capabilities/readcontract --> capabilities/libs/testutils
	click capabilities/readcontract href "https://github.com/smartcontractkit/capabilities"
	capabilities/streams --> capabilities/libs/loopserver
	capabilities/streams --> capabilities/libs/testutils
	click capabilities/streams href "https://github.com/smartcontractkit/capabilities"
	chain-selectors
	click chain-selectors href "https://github.com/smartcontractkit/chain-selectors"
	chainlink-automation --> chainlink-common
	click chainlink-automation href "https://github.com/smartcontractkit/chainlink-automation"
	chainlink-ccip --> chain-selectors
	chainlink-ccip --> chainlink-common
	click chainlink-ccip href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-common --> grpc-proxy
	chainlink-common --> libocr
	click chainlink-common href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-cosmos --> chainlink-common
	click chainlink-cosmos href "https://github.com/smartcontractkit/chainlink-cosmos"
	chainlink-data-streams --> chainlink-common
	click chainlink-data-streams href "https://github.com/smartcontractkit/chainlink-data-streams"
	chainlink-feeds --> chainlink-common
	click chainlink-feeds href "https://github.com/smartcontractkit/chainlink-feeds"
	chainlink-protos/orchestrator --> wsrpc
	click chainlink-protos/orchestrator href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-solana --> chainlink-common
	click chainlink-solana href "https://github.com/smartcontractkit/chainlink-solana"
	chainlink-starknet/relayer --> chainlink-common
	click chainlink-starknet/relayer href "https://github.com/smartcontractkit/chainlink-starknet"
	chainlink/v2 --> chainlink-automation
	chainlink/v2 --> chainlink-ccip
	chainlink/v2 --> chainlink-cosmos
	chainlink/v2 --> chainlink-data-streams
	chainlink/v2 --> chainlink-feeds
	chainlink/v2 --> chainlink-protos/orchestrator
	chainlink/v2 --> chainlink-solana
	chainlink/v2 --> chainlink-starknet/relayer
	chainlink/v2 --> tdh2/go/ocr2/decryptionplugin
	click chainlink/v2 href "https://github.com/smartcontractkit/chainlink"
	grpc-proxy
	click grpc-proxy href "https://github.com/smartcontractkit/grpc-proxy"
	libocr
	click libocr href "https://github.com/smartcontractkit/libocr"
	tdh2/go/ocr2/decryptionplugin --> libocr
	tdh2/go/ocr2/decryptionplugin --> tdh2/go/tdh2
	click tdh2/go/ocr2/decryptionplugin href "https://github.com/smartcontractkit/tdh2"
	tdh2/go/tdh2
	click tdh2/go/tdh2 href "https://github.com/smartcontractkit/tdh2"
	wsrpc
	click wsrpc href "https://github.com/smartcontractkit/wsrpc"

	subgraph capabilities-repo[capabilities]
		 capabilities/cron
		 capabilities/devenv
		 capabilities/integration_tests
		 capabilities/kvstore
		 capabilities/libs/cll
		 capabilities/libs/loopserver
		 capabilities/libs/testutils
		 capabilities/readcontract
		 capabilities/streams
	end
	click capabilities-repo href "https://github.com/smartcontractkit/capabilities"

	subgraph tdh2-repo[tdh2]
		 tdh2/go/ocr2/decryptionplugin
		 tdh2/go/tdh2
	end
	click tdh2-repo href "https://github.com/smartcontractkit/tdh2"

	classDef outline stroke-dasharray:6,fill:none;
	class capabilities-repo,tdh2-repo outline
```
