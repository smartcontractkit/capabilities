package capabilities

// Required so that the tool is available to be run in go generate below.
import _ "github.com/smartcontractkit/chainlink-common/pkg/capabilities/cli/cmd"

//go:generate go run github.com/smartcontractkit/chainlink-common/pkg/capabilities/cli/cmd/generate-types --dir $GOFILE --extra_urls https://raw.githubusercontent.com/smartcontractkit/chainlink-common/main/pkg/capabilities/consensus/ocr3/ocr3cap/ocr3cap_common-schema.json
