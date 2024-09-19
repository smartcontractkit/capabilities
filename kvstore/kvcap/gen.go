package kvcap

import _ "github.com/smartcontractkit/chainlink-common/pkg/capabilities/cli/cmd" // Required so that the tool is available to be run in go generate below.

//go:generate go run github.com/smartcontractkit/chainlink-common/pkg/capabilities/cli/cmd/generate-types --dir $GOFILE --extra_urls https://raw.githubusercontent.com/smartcontractkit/chainlink-common/refs/heads/main/pkg/capabilities/consensus/ocr3/ocr3cap/ocr3cap_common-schema.json
