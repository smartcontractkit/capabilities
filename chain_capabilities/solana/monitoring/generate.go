//go:generate protoc --proto_path=../../../ --go_out=paths=source_relative:../../../ --go-grpc_out=paths=source_relative:../../../ chain_capabilities/solana/monitoring/write_report.proto
package monitoring
