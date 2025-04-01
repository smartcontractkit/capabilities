## Capabilities modules and org dependencies
```mermaid
flowchart LR

	capabilities/cron --> capabilities/libs/loopserver
	click capabilities/cron href "https://github.com/smartcontractkit/capabilities"
	capabilities/kvstore --> capabilities/libs/loopserver
	capabilities/kvstore --> capabilities/libs/testutils
	click capabilities/kvstore href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs/cll --> libocr
	click capabilities/libs/cll href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs/loopserver --> chainlink-common
	click capabilities/libs/loopserver href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs/testutils --> chainlink-common
	click capabilities/libs/testutils href "https://github.com/smartcontractkit/capabilities"
	capabilities/loadtestwritetarget --> capabilities/libs/loopserver
	click capabilities/loadtestwritetarget href "https://github.com/smartcontractkit/capabilities"
	capabilities/readcontract --> capabilities/libs/loopserver
	capabilities/readcontract --> capabilities/libs/testutils
	click capabilities/readcontract href "https://github.com/smartcontractkit/capabilities"
	capabilities/streams --> capabilities/libs/loopserver
	capabilities/streams --> capabilities/libs/testutils
	click capabilities/streams href "https://github.com/smartcontractkit/capabilities"
	capabilities/workflowevent --> capabilities/libs/loopserver
	capabilities/workflowevent --> capabilities/libs/testutils
	click capabilities/workflowevent href "https://github.com/smartcontractkit/capabilities"
	capabilities/workflows --> chainlink-common
	click capabilities/workflows href "https://github.com/smartcontractkit/capabilities"
	chainlink-aptos/relayer --> chainlink-common
	click chainlink-aptos/relayer href "https://github.com/smartcontractkit/chainlink-aptos"
	chainlink-common --> grpc-proxy
	chainlink-common --> libocr
	click chainlink-common href "https://github.com/smartcontractkit/chainlink-common"
	grpc-proxy
	click grpc-proxy href "https://github.com/smartcontractkit/grpc-proxy"
	libocr
	click libocr href "https://github.com/smartcontractkit/libocr"

	subgraph capabilities-repo[capabilities]
		 capabilities/cron
		 capabilities/kvstore
		 capabilities/libs/cll
		 capabilities/libs/loopserver
		 capabilities/libs/testutils
		 capabilities/loadtestwritetarget
		 capabilities/readcontract
		 capabilities/streams
		 capabilities/workflowevent
		 capabilities/workflows
	end
	click capabilities-repo href "https://github.com/smartcontractkit/capabilities"

	classDef outline stroke-dasharray:6,fill:none;
	class capabilities-repo outline
```
