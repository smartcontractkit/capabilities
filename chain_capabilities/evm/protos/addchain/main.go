// Command addchain adds an EVM chain to this capability's client.proto.
//
// The chain selectors a workflow may name are a label on the capability's
// methods, defaulted in client.proto - so a chain the CRE supports is a chain
// listed there, and adding one by hand means finding a sorted array of a few
// hundred entries and getting the formatting right. This does that edit.
//
// It is chainlink-protos' cre/go/tools/add-evm-chain, brought here with the proto
// it edits: this capability owns its copy of client.proto (see the package
// beside this one), so the tool that maintains the list has to be the one that
// knows where this copy is.
//
// Usage, from anywhere in this module:
//
//	go run ./protos/addchain -selector 16015286601757825753
//	go generate ./protos/gen
//
// The second command is what makes the edit mean anything: the proto is what the
// generated Go is generated from, and nothing reads the proto at runtime.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	chain_selectors "github.com/smartcontractkit/chain-selectors"

	"github.com/smartcontractkit/capabilities/libs/standalone/protohelpers/generator"
)

// protoFile is the proto the chain list lives in, under this module's protos
// directory - the same place the generator reads them from.
const protoFile = "client.proto"

func main() {
	selector := flag.Uint64("selector", 0, "chain selector value (required)")
	path := flag.String("proto", "", "proto file to edit; defaults to this module's "+filepath.Join(generator.Protos, protoFile))
	flag.Parse()

	if *selector == 0 {
		fatal("selector is required")
	}

	protoPath := *path
	if protoPath == "" {
		resolved, err := defaultProtoPath()
		if err != nil {
			fatal("%v", err)
		}
		protoPath = resolved
	}

	// Look up chain ID from selector
	chainId, err := chain_selectors.ChainIdFromSelector(*selector)
	if err != nil {
		fatal("selector %d not found: %v", *selector, err)
	}

	// Get chain name from chain ID
	chainName, err := chain_selectors.NameFromChainId(chainId)
	if err != nil {
		fatal("failed to get chain name for chain ID %d: %v", chainId, err)
	}

	// Read proto file
	content, err := os.ReadFile(protoPath)
	if err != nil {
		fatal("failed to read %s: %v", protoPath, err)
	}

	// Check if already exists
	if strings.Contains(string(content), fmt.Sprintf(`key: "%s"`, chainName)) {
		fmt.Printf("chain %s already exists\n", chainName)
		return
	}

	// Parse, add, sort, rebuild
	newContent, err := addChain(string(content), chainName, *selector)
	if err != nil {
		fatal("failed to add chain: %v", err)
	}

	if err := os.WriteFile(protoPath, []byte(newContent), 0600); err != nil {
		fatal("failed to write file: %v", err)
	}

	fmt.Printf("added %s (selector: %d) to %s\n", chainName, *selector, protoPath)
	fmt.Println("run \"go generate ./protos/gen\" to regenerate the Go this proto describes")
}

// defaultProtoPath finds the proto relative to the module rather than to the
// working directory, so this can be run from anywhere under it - the same rule
// the generator beside it follows.
func defaultProtoPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to read the working directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, generator.Protos, protoFile), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above the working directory, so there is no module to find %s in; pass -proto", protoFile)
		}
		dir = parent
	}
}

func addChain(content, name string, selector uint64) (string, error) {
	// Find defaults array
	re := regexp.MustCompile(`defaults:\s*\[([\s\S]*?)\n\s*\]`)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return "", fmt.Errorf("defaults array not found")
	}

	// Parse entries
	entryRe := regexp.MustCompile(`\{\s*key:\s*"([^"]+)"\s*value:\s*(\d+)\s*\}`)
	entries := entryRe.FindAllStringSubmatch(match[1], -1)

	type entry struct {
		key string
		val uint64
	}
	var list []entry
	for _, e := range entries {
		v, _ := strconv.ParseUint(e[2], 10, 64)
		list = append(list, entry{e[1], v})
	}
	list = append(list, entry{name, selector})

	sort.Slice(list, func(i, j int) bool { return list[i].key < list[j].key })

	var b strings.Builder
	b.WriteString("defaults: [\n")
	for i, e := range list {
		b.WriteString(fmt.Sprintf("            {\n              key: \"%s\"\n              value: %d\n            }", e.key, e.val))
		if i < len(list)-1 {
			b.WriteString(",\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString("          ]")

	return re.ReplaceAllString(content, b.String()), nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
