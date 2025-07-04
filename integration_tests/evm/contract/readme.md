## How to build the contract and generate Go bindings

From the `integration_tests/evm/` directory, run the following commands:

### 1 Compile the contract
The first command uses solc (the Solidity compiler) to generate the ABI and binary files for the MessageEmitter.sol contract and outputs them to the integration_tests/evm/contract/ directory.
```bash
solc --abi --bin --overwrite -o ./contract/ ./contract/MessageEmitter.sol
```

### 2 Generate Go bindings
The second command uses abigen (Go Ethereum's ABI generator) to generate Go bindings from the compiled contract's ABI and binary files, placing the resulting Go file in the same directory. This allows Go code to interact with the smart contract.
```bash
abigen --bin=./contract/MessageEmitter.bin --abi=./contract/MessageEmitter.abi --pkg=contract --out=./contract/MessageEmitter.go
```

