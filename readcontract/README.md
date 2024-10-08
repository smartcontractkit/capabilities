Capability to read a contract on a given chain.  The chain is configured by providing a json string in the config parameter of the standard capability, for example:


```json
{
  "chainId": 1,
  "network": "testnet"
}
```

To read the latest value of a given contract create and execute a `CapabilityRequest` as follows :

```go
config := readcontractcap.Config{ContractReaderConfig: "<contract reader config goes here>"}

inputs := readcontractcap.Input{
    ReadIdentifier:  "TestReadIdentifier",
    Address:         "0x123",
    ConfidenceLevel: "finalized",
    Params: readcontractcap.InputParams{
        "param1": "value1",
        "param2": "value2",
    },
}

requestConfig, err := values.WrapMap(config)
// check err
requestInputs, err := values.WrapMap(inputs)
// check err

request := capabilities.CapabilityRequest{
		Config: requestConfig,
		Inputs: requestInputs,
	}

response, err : readcapabiltiyaction.Execute(ctx, request)
// check err

latestValue := response.Value.Underlying["LatestValue"]
var result someType
err = latestValue.UnwrapTo(&result)
//check err

```

The `readIdentifier` is typically the method name on the contract at `address`.  The `contractReaderConfig' is specific the contract reader being used.



