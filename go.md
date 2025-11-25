## Capabilities modules and org dependencies
```mermaid
flowchart LR

	capabilities/capabilitywatcher --> capabilities/libs
	click capabilities/capabilitywatcher href "https://github.com/smartcontractkit/capabilities"
	capabilities/chain_capabilities/evm --> capabilities/libs
	capabilities/chain_capabilities/evm --> chainlink-evm
	click capabilities/chain_capabilities/evm href "https://github.com/smartcontractkit/capabilities"
	capabilities/consensus --> capabilities/libs
	capabilities/consensus --> cre-sdk-go
	click capabilities/consensus href "https://github.com/smartcontractkit/capabilities"
	capabilities/cron --> capabilities/libs
	click capabilities/cron href "https://github.com/smartcontractkit/capabilities"
	capabilities/http_action --> capabilities/libs
	click capabilities/http_action href "https://github.com/smartcontractkit/capabilities"
	capabilities/http_trigger --> capabilities/libs
	click capabilities/http_trigger href "https://github.com/smartcontractkit/capabilities"
	capabilities/integration_tests --> capabilities/chain_capabilities/evm
	capabilities/integration_tests --> capabilities/http_action
	capabilities/integration_tests --> capabilities/http_trigger
	capabilities/integration_tests --> capabilities/loadtestwritetarget
	capabilities/integration_tests --> chainlink/v2
	capabilities/integration_tests --> cre-sdk-go/capabilities/blockchain/evm
	click capabilities/integration_tests href "https://github.com/smartcontractkit/capabilities"
	capabilities/kvstore --> capabilities/libs
	click capabilities/kvstore href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs --> chainlink-common
	click capabilities/libs href "https://github.com/smartcontractkit/capabilities"
	capabilities/loadtestwritetarget --> capabilities/libs
	click capabilities/loadtestwritetarget href "https://github.com/smartcontractkit/capabilities"
	capabilities/mock --> capabilities/libs
	click capabilities/mock href "https://github.com/smartcontractkit/capabilities"
	capabilities/p2psigner --> capabilities/libs
	click capabilities/p2psigner href "https://github.com/smartcontractkit/capabilities"
	capabilities/readcontract --> capabilities/libs
	click capabilities/readcontract href "https://github.com/smartcontractkit/capabilities"
	capabilities/workflowevent --> capabilities/libs
	click capabilities/workflowevent href "https://github.com/smartcontractkit/capabilities"
	capabilities/workflows --> chainlink-common
	click capabilities/workflows href "https://github.com/smartcontractkit/capabilities"
	capabilities/workflows/readbalancesgen --> chainlink-common
	click capabilities/workflows/readbalancesgen href "https://github.com/smartcontractkit/capabilities"
	chain-selectors
	click chain-selectors href "https://github.com/smartcontractkit/chain-selectors"
	chainlink-aptos --> chainlink-common
	click chainlink-aptos href "https://github.com/smartcontractkit/chainlink-aptos"
	chainlink-automation --> chainlink-common
	click chainlink-automation href "https://github.com/smartcontractkit/chainlink-automation"
	chainlink-ccip --> chainlink-common
	chainlink-ccip --> chainlink-protos/rmn/v1.6/go
	click chainlink-ccip href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-ccip/ccv/chains/evm
	click chainlink-ccip/ccv/chains/evm href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-ccip/chains/solana --> chainlink-ccip
	chainlink-ccip/chains/solana --> chainlink-ccip/chains/solana/gobindings
	click chainlink-ccip/chains/solana href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-ccip/chains/solana/gobindings
	click chainlink-ccip/chains/solana/gobindings href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-ccv --> chainlink-ccip/ccv/chains/evm
	chainlink-ccv --> chainlink-evm
	chainlink-ccv --> chainlink-protos/chainlink-ccv/go
	click chainlink-ccv href "https://github.com/smartcontractkit/chainlink-ccv"
	chainlink-common --> chain-selectors
	chainlink-common --> chainlink-common/pkg/chipingress
	chainlink-common --> chainlink-protos/billing/go
	chainlink-common --> chainlink-protos/cre/go
	chainlink-common --> chainlink-protos/linking-service/go
	chainlink-common --> chainlink-protos/storage-service
	chainlink-common --> freeport
	chainlink-common --> grpc-proxy
	chainlink-common --> libocr
	click chainlink-common href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-common/pkg/chipingress
	click chainlink-common/pkg/chipingress href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-common/pkg/monitoring
	click chainlink-common/pkg/monitoring href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-common/pkg/values
	click chainlink-common/pkg/values href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-data-streams --> chainlink-common
	click chainlink-data-streams href "https://github.com/smartcontractkit/chainlink-data-streams"
	chainlink-evm --> chainlink-evm/gethwrappers
	chainlink-evm --> chainlink-framework/capabilities
	chainlink-evm --> chainlink-framework/chains
	chainlink-evm --> chainlink-protos/svr
	chainlink-evm --> chainlink-tron/relayer
	click chainlink-evm href "https://github.com/smartcontractkit/chainlink-evm"
	chainlink-evm/gethwrappers
	click chainlink-evm/gethwrappers href "https://github.com/smartcontractkit/chainlink-evm"
	chainlink-feeds --> chainlink-common
	click chainlink-feeds href "https://github.com/smartcontractkit/chainlink-feeds"
	chainlink-framework/capabilities --> chainlink-common
	click chainlink-framework/capabilities href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-framework/chains --> chainlink-framework/multinode
	click chainlink-framework/chains href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-framework/metrics --> chainlink-common
	click chainlink-framework/metrics href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-framework/multinode --> chainlink-framework/metrics
	click chainlink-framework/multinode href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-protos/billing/go --> chainlink-protos/workflows/go
	click chainlink-protos/billing/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/chainlink-ccv/go
	click chainlink-protos/chainlink-ccv/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/cre/go
	click chainlink-protos/cre/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/linking-service/go
	click chainlink-protos/linking-service/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/orchestrator --> wsrpc
	click chainlink-protos/orchestrator href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/rmn/v1.6/go
	click chainlink-protos/rmn/v1.6/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/storage-service
	click chainlink-protos/storage-service href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/svr
	click chainlink-protos/svr href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/workflows/go
	click chainlink-protos/workflows/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-solana --> chainlink-ccip/chains/solana
	chainlink-solana --> chainlink-common/pkg/monitoring
	chainlink-solana --> chainlink-framework/capabilities
	chainlink-solana --> chainlink-framework/multinode
	click chainlink-solana href "https://github.com/smartcontractkit/chainlink-solana"
	chainlink-sui --> chainlink-aptos
	chainlink-sui --> chainlink-ccip
	chainlink-sui --> chainlink-common/pkg/values
	click chainlink-sui href "https://github.com/smartcontractkit/chainlink-sui"
	chainlink-ton --> chainlink-ccip
	click chainlink-ton href "https://github.com/smartcontractkit/chainlink-ton"
	chainlink-tron/relayer --> chainlink-common
	click chainlink-tron/relayer href "https://github.com/smartcontractkit/chainlink-tron"
	chainlink/v2 --> chainlink-automation
	chainlink/v2 --> chainlink-ccv
	chainlink/v2 --> chainlink-data-streams
	chainlink/v2 --> chainlink-feeds
	chainlink/v2 --> chainlink-protos/orchestrator
	chainlink/v2 --> chainlink-solana
	chainlink/v2 --> chainlink-sui
	chainlink/v2 --> chainlink-ton
	chainlink/v2 --> cre-sdk-go/capabilities/networking/http
	chainlink/v2 --> cre-sdk-go/capabilities/scheduler/cron
	chainlink/v2 --> quarantine
	chainlink/v2 --> smdkg
	chainlink/v2 --> tdh2/go/ocr2/decryptionplugin
	click chainlink/v2 href "https://github.com/smartcontractkit/chainlink"
	cre-sdk-go --> chainlink-protos/cre/go
	click cre-sdk-go href "https://github.com/smartcontractkit/cre-sdk-go"
	cre-sdk-go/capabilities/blockchain/evm --> cre-sdk-go
	click cre-sdk-go/capabilities/blockchain/evm href "https://github.com/smartcontractkit/cre-sdk-go"
	cre-sdk-go/capabilities/networking/http
	click cre-sdk-go/capabilities/networking/http href "https://github.com/smartcontractkit/cre-sdk-go"
	cre-sdk-go/capabilities/scheduler/cron --> cre-sdk-go
	click cre-sdk-go/capabilities/scheduler/cron href "https://github.com/smartcontractkit/cre-sdk-go"
	freeport
	click freeport href "https://github.com/smartcontractkit/freeport"
	go-sumtype2
	click go-sumtype2 href "https://github.com/smartcontractkit/go-sumtype2"
	grpc-proxy
	click grpc-proxy href "https://github.com/smartcontractkit/grpc-proxy"
	libocr --> go-sumtype2
	click libocr href "https://github.com/smartcontractkit/libocr"
	quarantine
	click quarantine href "https://github.com/smartcontractkit/quarantine"
	smdkg --> libocr
	smdkg --> tdh2/go/tdh2
	click smdkg href "https://github.com/smartcontractkit/smdkg"
	tdh2/go/ocr2/decryptionplugin --> libocr
	tdh2/go/ocr2/decryptionplugin --> tdh2/go/tdh2
	click tdh2/go/ocr2/decryptionplugin href "https://github.com/smartcontractkit/tdh2"
	tdh2/go/tdh2
	click tdh2/go/tdh2 href "https://github.com/smartcontractkit/tdh2"
	wsrpc
	click wsrpc href "https://github.com/smartcontractkit/wsrpc"

	subgraph capabilities-repo[capabilities]
		 capabilities/capabilitywatcher
		 capabilities/chain_capabilities/evm
		 capabilities/consensus
		 capabilities/cron
		 capabilities/http_action
		 capabilities/http_trigger
		 capabilities/integration_tests
		 capabilities/kvstore
		 capabilities/libs
		 capabilities/loadtestwritetarget
		 capabilities/mock
		 capabilities/p2psigner
		 capabilities/readcontract
		 capabilities/workflowevent
		 capabilities/workflows
		 capabilities/workflows/readbalancesgen
	end
	click capabilities-repo href "https://github.com/smartcontractkit/capabilities"

	subgraph chainlink-ccip-repo[chainlink-ccip]
		 chainlink-ccip
		 chainlink-ccip/ccv/chains/evm
		 chainlink-ccip/chains/solana
		 chainlink-ccip/chains/solana/gobindings
	end
	click chainlink-ccip-repo href "https://github.com/smartcontractkit/chainlink-ccip"

	subgraph chainlink-common-repo[chainlink-common]
		 chainlink-common
		 chainlink-common/pkg/chipingress
		 chainlink-common/pkg/monitoring
		 chainlink-common/pkg/values
	end
	click chainlink-common-repo href "https://github.com/smartcontractkit/chainlink-common"

	subgraph chainlink-evm-repo[chainlink-evm]
		 chainlink-evm
		 chainlink-evm/gethwrappers
	end
	click chainlink-evm-repo href "https://github.com/smartcontractkit/chainlink-evm"

	subgraph chainlink-framework-repo[chainlink-framework]
		 chainlink-framework/capabilities
		 chainlink-framework/chains
		 chainlink-framework/metrics
		 chainlink-framework/multinode
	end
	click chainlink-framework-repo href "https://github.com/smartcontractkit/chainlink-framework"

	subgraph chainlink-protos-repo[chainlink-protos]
		 chainlink-protos/billing/go
		 chainlink-protos/chainlink-ccv/go
		 chainlink-protos/cre/go
		 chainlink-protos/linking-service/go
		 chainlink-protos/orchestrator
		 chainlink-protos/rmn/v1.6/go
		 chainlink-protos/storage-service
		 chainlink-protos/svr
		 chainlink-protos/workflows/go
	end
	click chainlink-protos-repo href "https://github.com/smartcontractkit/chainlink-protos"

	subgraph cre-sdk-go-repo[cre-sdk-go]
		 cre-sdk-go
		 cre-sdk-go/capabilities/blockchain/evm
		 cre-sdk-go/capabilities/networking/http
		 cre-sdk-go/capabilities/scheduler/cron
	end
	click cre-sdk-go-repo href "https://github.com/smartcontractkit/cre-sdk-go"

	subgraph tdh2-repo[tdh2]
		 tdh2/go/ocr2/decryptionplugin
		 tdh2/go/tdh2
	end
	click tdh2-repo href "https://github.com/smartcontractkit/tdh2"

	classDef outline stroke-dasharray:6,fill:none;
	class capabilities-repo,chainlink-ccip-repo,chainlink-common-repo,chainlink-evm-repo,chainlink-framework-repo,chainlink-protos-repo,cre-sdk-go-repo,tdh2-repo outline
```
