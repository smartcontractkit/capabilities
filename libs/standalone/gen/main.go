package main

import (
	"bytes"
	_ "embed"
	"log"
	"text/template"

	"github.com/smartcontractkit/chainlink-common/pkg/utils/codegen"
)

//go:embed bootstrap.go.tmpl
var bootstrapGo string

const toolName = "github.com/smartcontractkit/capabilities/libs/standalone/gen"

// maxDeps is the highest number of dependencies a generated Run helper accepts.
// Run1..Run{maxDeps} are generated.
const maxDeps = 10

func main() {
	// rangeNum(maxDeps+1)[1:] yields 1..maxDeps so we skip the zero-dependency case.
	nums := rangeNum(maxDeps + 1)[1:]

	t, err := template.New("bootstrap").Funcs(template.FuncMap{"RangeNum": rangeNum}).Parse(bootstrapGo)
	if err != nil {
		log.Fatal(err)
	}

	results := bytes.Buffer{}
	if err = t.Execute(&results, nums); err != nil {
		log.Fatal(err)
	}

	files := map[string]string{"bootstrap_gen.go": results.String()}
	if err = codegen.WriteFiles(".", "github.com/smartcontractkit", toolName, files); err != nil {
		log.Fatal(err)
	}
}

// rangeNum returns a slice [0, 1, ..., num-1], used by the template to iterate type parameters and arguments.
func rangeNum(num int) []int {
	nums := make([]int, num)
	for i := range num {
		nums[i] = i
	}

	return nums
}
