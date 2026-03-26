.PHONY: modgraph
modgraph:
	go install github.com/jmank88/gomods@v0.1.6
	go install github.com/jmank88/modgraph@v0.1.1
	./modgraph > go.md

.PHONY: gomods
gomods: ## Install gomods
	go install github.com/jmank88/gomods@v0.1.6

.PHONY: gomodtidy
gomodtidy: gomods ## Run go mod tidy on all modules.
	gomods tidy

.PHONY: tidy
tidy: gomodtidy gomods ## Tidy all modules and add to git.
	git add '**go.*'

PROTOC_GEN_GO_VERSION := 1.36.10
.PHONY: protoc
protoc: ## Install protoc and protoc-gen-go
	./script/install-protoc.sh 29.3
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v$(PROTOC_GEN_GO_VERSION)

MOCKERY_VERSION := 2.53.5
.PHONY: mockery
mockery: ## Install mockery at the version specified in .tool-versions
	go install github.com/vektra/mockery/v2@v$(MOCKERY_VERSION)

.PHONY: generate
generate: protoc mockery gomods ## Execute all go:generate commands (including proto generation).
	## Updating PATH makes sure that go:generate uses the version of protoc installed by the protoc make command.
	export PATH="$(HOME)/.local/bin:$(PATH)"; gomods -w go generate -x ./...

.PHONY: update-common-capabilities
update-common-capabilities: ## Update chain_capabilities/common in aptos/evm/solana. Usage: make update-common-capabilities REF=<branch-or-commit>
	./script/update-common-capabilities.sh $(REF)

.PHONY: help
help: ## Display this help screen.
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
