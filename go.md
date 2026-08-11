## Capabilities modules and org dependencies
```mermaid
flowchart LR

	capabilities/chain_capabilities/aptos --> capabilities/chain_capabilities/common
	capabilities/chain_capabilities/aptos --> chainlink-aptos
	click capabilities/chain_capabilities/aptos href "https://github.com/smartcontractkit/capabilities"
	capabilities/chain_capabilities/common --> capabilities/libs
	click capabilities/chain_capabilities/common href "https://github.com/smartcontractkit/capabilities"
	capabilities/chain_capabilities/evm --> capabilities/chain_capabilities/common
	capabilities/chain_capabilities/evm --> chainlink-evm
	click capabilities/chain_capabilities/evm href "https://github.com/smartcontractkit/capabilities"
	capabilities/chain_capabilities/solana --> capabilities/chain_capabilities/common
	capabilities/chain_capabilities/solana --> chainlink-solana/contracts
	click capabilities/chain_capabilities/solana href "https://github.com/smartcontractkit/capabilities"
	capabilities/chain_capabilities/stellar --> capabilities/chain_capabilities/common
	capabilities/chain_capabilities/stellar --> chainlink-framework/multinode
	click capabilities/chain_capabilities/stellar href "https://github.com/smartcontractkit/capabilities"
	capabilities/consensus --> capabilities/libs
	capabilities/consensus --> cre-sdk-go
	click capabilities/consensus href "https://github.com/smartcontractkit/capabilities"
	capabilities/cron --> capabilities/libs
	click capabilities/cron href "https://github.com/smartcontractkit/capabilities"
	capabilities/http_action --> capabilities/libs
	click capabilities/http_action href "https://github.com/smartcontractkit/capabilities"
	capabilities/http_trigger --> capabilities/libs
	click capabilities/http_trigger href "https://github.com/smartcontractkit/capabilities"
	capabilities/libs --> chainlink-common
	click capabilities/libs href "https://github.com/smartcontractkit/capabilities"
	chain-selectors
	click chain-selectors href "https://github.com/smartcontractkit/chain-selectors"
	chainlink-aptos --> chainlink-common
	click chainlink-aptos href "https://github.com/smartcontractkit/chainlink-aptos"
	chainlink-ccip --> chainlink-common
	chainlink-ccip --> chainlink-evm/gethwrappers/helpers
	chainlink-ccip --> chainlink-protos/rmn/v1.6/go
	click chainlink-ccip href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-ccip/chains/solana --> chainlink-ccip/chains/solana/gobindings
	chainlink-ccip/chains/solana --> chainlink-common
	click chainlink-ccip/chains/solana href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-ccip/chains/solana/gobindings
	click chainlink-ccip/chains/solana/gobindings href "https://github.com/smartcontractkit/chainlink-ccip"
	chainlink-common --> chainlink-common/pkg/chipingress
	chainlink-common --> chainlink-protos/billing/go
	chainlink-common --> chainlink-protos/cre/go
	chainlink-common --> chainlink-protos/linking-service/go
	chainlink-common --> chainlink-protos/metering/go
	chainlink-common --> chainlink-protos/node-platform
	chainlink-common --> chainlink-protos/storage-service
	chainlink-common --> chainlink-protos/workflows/go
	chainlink-common --> freeport
	chainlink-common --> grpc-proxy
	chainlink-common --> libocr
	click chainlink-common href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-common/keystore --> chainlink-common
	chainlink-common/keystore --> smdkg
	chainlink-common/keystore --> wsrpc
	click chainlink-common/keystore href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-common/pkg/chipingress
	click chainlink-common/pkg/chipingress href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-common/pkg/monitoring
	click chainlink-common/pkg/monitoring href "https://github.com/smartcontractkit/chainlink-common"
	chainlink-data-streams
	click chainlink-data-streams href "https://github.com/smartcontractkit/chainlink-data-streams"
	chainlink-deployments-framework
	click chainlink-deployments-framework href "https://github.com/smartcontractkit/chainlink-deployments-framework"
	chainlink-evm --> chainlink-common/keystore
	chainlink-evm --> chainlink-data-streams
	chainlink-evm --> chainlink-evm/gethwrappers
	chainlink-evm --> chainlink-framework/capabilities
	chainlink-evm --> chainlink-framework/chains
	chainlink-evm --> chainlink-protos/svr
	chainlink-evm --> chainlink-tron/relayer
	click chainlink-evm href "https://github.com/smartcontractkit/chainlink-evm"
	chainlink-evm/gethwrappers
	click chainlink-evm/gethwrappers href "https://github.com/smartcontractkit/chainlink-evm"
	chainlink-evm/gethwrappers/helpers
	click chainlink-evm/gethwrappers/helpers href "https://github.com/smartcontractkit/chainlink-evm"
	chainlink-framework/capabilities
	click chainlink-framework/capabilities href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-framework/chains --> chainlink-framework/multinode
	click chainlink-framework/chains href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-framework/metrics --> chainlink-common
	click chainlink-framework/metrics href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-framework/multinode --> chainlink-framework/metrics
	click chainlink-framework/multinode href "https://github.com/smartcontractkit/chainlink-framework"
	chainlink-protos/billing/go
	click chainlink-protos/billing/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/cre/go --> chain-selectors
	click chainlink-protos/cre/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/linking-service/go
	click chainlink-protos/linking-service/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/metering/go
	click chainlink-protos/metering/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/node-platform
	click chainlink-protos/node-platform href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/rmn/v1.6/go
	click chainlink-protos/rmn/v1.6/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/storage-service
	click chainlink-protos/storage-service href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/svr
	click chainlink-protos/svr href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-protos/workflows/go
	click chainlink-protos/workflows/go href "https://github.com/smartcontractkit/chainlink-protos"
	chainlink-solana --> chainlink-ccip
	chainlink-solana --> chainlink-ccip/chains/solana
	chainlink-solana --> chainlink-common/keystore
	chainlink-solana --> chainlink-common/pkg/monitoring
	chainlink-solana --> chainlink-framework/multinode
	click chainlink-solana href "https://github.com/smartcontractkit/chainlink-solana"
	chainlink-solana/contracts --> chainlink-deployments-framework
	chainlink-solana/contracts --> chainlink-solana
	chainlink-solana/contracts --> chainlink-testing-framework/framework
	chainlink-solana/contracts --> chainlink-tron/relayer
	click chainlink-solana/contracts href "https://github.com/smartcontractkit/chainlink-solana"
	chainlink-testing-framework/framework
	click chainlink-testing-framework/framework href "https://github.com/smartcontractkit/chainlink-testing-framework"
	chainlink-tron/relayer
	click chainlink-tron/relayer href "https://github.com/smartcontractkit/chainlink-tron"
	cre-sdk-go --> chainlink-protos/cre/go
	click cre-sdk-go href "https://github.com/smartcontractkit/cre-sdk-go"
	freeport
	click freeport href "https://github.com/smartcontractkit/freeport"
	go-sumtype2
	click go-sumtype2 href "https://github.com/smartcontractkit/go-sumtype2"
	grpc-proxy
	click grpc-proxy href "https://github.com/smartcontractkit/grpc-proxy"
	libocr --> go-sumtype2
	click libocr href "https://github.com/smartcontractkit/libocr"
	smdkg
	click smdkg href "https://github.com/smartcontractkit/smdkg"
	wsrpc
	click wsrpc href "https://github.com/smartcontractkit/wsrpc"

	subgraph capabilities-repo[capabilities]
		 capabilities/chain_capabilities/aptos
		 capabilities/chain_capabilities/common
		 capabilities/chain_capabilities/evm
		 capabilities/chain_capabilities/solana
		 capabilities/chain_capabilities/stellar
		 capabilities/consensus
		 capabilities/cron
		 capabilities/http_action
		 capabilities/http_trigger
		 capabilities/libs
	end
	click capabilities-repo href "https://github.com/smartcontractkit/capabilities"

	subgraph chainlink-ccip-repo[chainlink-ccip]
		 chainlink-ccip
		 chainlink-ccip/chains/solana
		 chainlink-ccip/chains/solana/gobindings
	end
	click chainlink-ccip-repo href "https://github.com/smartcontractkit/chainlink-ccip"

	subgraph chainlink-common-repo[chainlink-common]
		 chainlink-common
		 chainlink-common/keystore
		 chainlink-common/pkg/chipingress
		 chainlink-common/pkg/monitoring
	end
	click chainlink-common-repo href "https://github.com/smartcontractkit/chainlink-common"

	subgraph chainlink-evm-repo[chainlink-evm]
		 chainlink-evm
		 chainlink-evm/gethwrappers
		 chainlink-evm/gethwrappers/helpers
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
		 chainlink-protos/cre/go
		 chainlink-protos/linking-service/go
		 chainlink-protos/metering/go
		 chainlink-protos/node-platform
		 chainlink-protos/rmn/v1.6/go
		 chainlink-protos/storage-service
		 chainlink-protos/svr
		 chainlink-protos/workflows/go
	end
	click chainlink-protos-repo href "https://github.com/smartcontractkit/chainlink-protos"

	subgraph chainlink-solana-repo[chainlink-solana]
		 chainlink-solana
		 chainlink-solana/contracts
	end
	click chainlink-solana-repo href "https://github.com/smartcontractkit/chainlink-solana"

	classDef outline stroke-dasharray:6,fill:none;
	class capabilities-repo,chainlink-ccip-repo,chainlink-common-repo,chainlink-evm-repo,chainlink-framework-repo,chainlink-protos-repo,chainlink-solana-repo outline
```
