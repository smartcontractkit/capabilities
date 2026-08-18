// Command gen generates the consensus capability's protos.
//
// It lives here, rather than in the module holding the generator, so that it is
// built from the generator - and the protoc plugins - that this capability's
// go.mod pins. Updating those is then this capability's own change, and a
// capability that has not made it keeps generating exactly what it did before.
package main

import (
	"fmt"
	"os"

	"github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/generator"
)

//go:generate go run .

func main() {
	// Any capability whose protos these import is named here, so that its
	// protos are compiled alongside and linked to the Go code it generated.
	if err := generator.Generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
