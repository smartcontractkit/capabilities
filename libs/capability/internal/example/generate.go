// Package example holds the capability newcapability scaffolds with a trigger and an action, which covers every
// method either generates. Its tests are the tests of what newcapability scaffolds and of the code protoc-gen-cre
// generates. It is scaffolded into this module, which generates its config's doc comments, once it is scaffolded.
package example

//go:generate go run ../../cmd/newcapability --force --no-module --trigger --action --out-dir .. --category example --name capability
//go:generate go -C ../.. run ./internal/docgen
