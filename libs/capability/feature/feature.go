// Package feature holds the options capability protos tag each field and method with, naming the feature it
// belongs to, and the option a capability releases features with. See feature.proto.
package feature

import _ "embed"

//go:generate sh -c "go build -o \"$DOLLAR{TMPDIR:-/tmp}/feature-protoc-gen-go\" google.golang.org/protobuf/cmd/protoc-gen-go && protoc --plugin=protoc-gen-go=\"$DOLLAR{TMPDIR:-/tmp}/feature-protoc-gen-go\" -I ../.. --go_out=../.. --go_opt=paths=source_relative capability/feature/feature.proto"

// Path is the path capability protos import feature.proto by.
const Path = "capability/feature/feature.proto"

// Proto is feature.proto, for the protoc runs that import it.
//
//go:embed feature.proto
var Proto []byte
