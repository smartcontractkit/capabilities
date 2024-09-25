Capability to read a contract on a given chain.  The chain is configured by providing a json string in the config parameter of the standard capability, for example:


```json
{
  "chainId": 1,
  "network": "testnet"
}
```

To read the latest value of a given contract create and execute a `CapabilityRequest` as follows :

```go

config := map[string]any{}
config["contractReaderConfig"] = "<contract reader config goes here>"

inputs := map[string]any{
	"readIdentifier":    "TestContract",
	"address": "0x123",
	"confidenceLevel": "finalized",
	"params": map[string]any{
		"param1": "value1",
		"param2": "value2",
	},
}

requestConfig, err := values.WrapMap(config)
requestInputs, err := values.WrapMap(inputs)

request := capabilities.CapabilityRequest{
		Config: requestConfig,
		Inputs: requestInputs,
	}

response, err : readcapabiltiyaction.Execute(ctx, request)
// check err

latestValue := response.Value.Underlying["latestValue"]
var result someType
err = latestValue.UnwrapTo(&result)
//check err

```

The `readIdentifier` is typically the method name on the contract at `address`.  The `contractReaderConfig' is specific the contract reader being used.



