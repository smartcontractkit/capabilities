//go:generate go run ./gen/main.go
//go:generate protoc --proto_path=../../../ --go_out=paths=source_relative:../../../ --go-grpc_out=paths=source_relative:../../../ chain_capabilities/evm/monitoring/read_actions.proto
package monitoring
