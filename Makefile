.PHONY: modgraph
modgraph:
	go install github.com/jmank88/gomods@v0.1.5
	go install github.com/jmank88/modgraph@8b0e2b07928b34a6e9d67639061ecd712ea8ee89
	./modgraph > go.md

.PHONY: install-protoc
install-protoc:
	script/install-protoc.sh 29.3 /
	go install google.golang.org/protobuf/cmd/protoc-gen-go@`go list -m -json google.golang.org/protobuf | jq -r .Version`
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	

.PHONY: generate
generate: install-protoc
	export PATH="$(HOME)/.local/bin:$(PATH)"; gomods -go generate -x ./...