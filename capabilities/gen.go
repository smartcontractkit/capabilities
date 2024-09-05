package capabilities

import _ "github.com/smartcontractkit/chainlink-common/pkg/capabilities/cli/cmd"

//go:generate go run github.com/smartcontractkit/chainlink-common/pkg/capabilities/cli/cmd/generate-types --dir $GOFILE --extra_urls https://raw.githubusercontent.com/smartcontractkit/chainlink-common/rtinianov_remoterefs/pkg/capabilities/consensus/ocr3/ocr3cap/ocr3cap_common-schema.json
