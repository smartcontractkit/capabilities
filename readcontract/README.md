An action capability for reading contract data from the blockchain.  Below is an example of a toml job spec required to deploy
the capability:

```toml
type = "standardcapabilities"
schemaVersion = 1
name = "readcontract-capability"
command="<deploymentpath>/readcontract"
config='''{"chainId":1337,"network":"evm"}'''
```

Below is an example of a workflow that utilises the capability. This shows the configuration required for the ValueSource 
contract used in the integration tests. Rather than hand coding the value required for `ContractReaderConfig` it is recommended
to use the `CreateContractReaderConfig` function in the integration tests project to generate the correct value.  


```yaml



name: "abcdef0123"
owner: "0x0100000000000000000000000000000000000001"
triggers:
  - id: "mock-trigger@1.0.0"
    ref: "trigger"
    config:
       mustputavaluehere_thisisabug: "true"

actions:
  - id: "read-contract-evm-1337@1.0.0"
    ref: "action2"
    inputs:
      $(trigger.outputs)
    config:
      ContractAddress: "0xEc870Fa3ea280C063b1cDb652C54DFf3c74DCd5b"
      ContractName: "ValueSource"
      ReadIdentifier: "0xEc870Fa3ea280C063b1cDb652C54DFf3c74DCd5b-ValueSource-GetValue"
      ContractReaderConfig: |
        {"contracts":{"ValueSource":{"contractABI":"[{\\\"inputs\\\":[],\\\"name\\\":\\\"GetValue\\\",\\\"outputs\\\":[{\\\"internalType\\\":\\\"int256[]\\\",\\\"name\\\":\\\"\\\",\\\"type\\\":\\\"int256[]\\\"}],\\\"stateMutability\\\":\\\"pure\\\",\\\"type\\\":\\\"function\\\"}]","contractPollingFilter":{"genericEventNames":null,"pollingFilter":{"topic2":null,"topic3":null,"topic4":null,"retention":"0s","maxLogsKept":0,"logsPerBlock":0}},"configs":{"GetValue":"{  \\\"chainSpecificName\\\": \\\"GetValue\\\"}"}}}}

targets:
  - id: "mock-target@1.0.0"
    ref: "target"
    inputs:
      $(action2.outputs)
    config:
      mustputavaluehere_thisisabug: "true"