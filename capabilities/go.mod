module github.com/smartcontractkit/capabilities/capabilities

go 1.23.0

require github.com/smartcontractkit/chainlink-common v0.2.2-0.20240830194402-e4cf200473ed

require (
	github.com/atombender/go-jsonschema v0.16.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.1.0 // indirect
	github.com/goccy/go-yaml v1.12.0 // indirect
	github.com/iancoleman/strcase v0.3.0 // indirect
	github.com/invopop/jsonschema v0.12.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/mr-tron/base58 v1.2.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sanity-io/litter v1.5.5 // indirect
	github.com/santhosh-tekuri/jsonschema/v5 v5.2.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/smartcontractkit/libocr v0.0.0-20240419185742-fd3cab206b2c // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	golang.org/x/exp v0.0.0-20240808152545-0cdaa3abc0fa // indirect
	golang.org/x/mod v0.20.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/tools v0.24.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// until merged upstream: https://github.com/omissis/go-jsonschema/pull/264
replace github.com/atombender/go-jsonschema => github.com/nolag/go-jsonschema v0.16.0-smallSizedInts
