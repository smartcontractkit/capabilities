# Blank Template

### Overview  
The **`New Workflow (blank)`** template provides a starting point with an empty `main.go` file, allowing you to define your own workflow. This guide walks you through setting up and running a simple workflow using the Chainlink CRE CLI.

### Configuration Files in CRE
The CRE CLI automatically manages its configuration using the following files:
- **`cre.yaml`** – A project settings file, located in the project root or any parent directory.
- **`workflow.yaml`** – A workflow-specific settings file, created inside each workflow directory.

## Configuring RPC Endpoints

To ensure your workflow operates correctly, you must specify RPC endpoints for the chains you interact with in the `cre.yaml` file. This is required for submitting transactions and reading blockchain state. Add your preferred RPCs under the `rpcs` section. Chainselector list can be found at: https://github.com/smartcontractkit/chain-selectors

```yaml
rpcs:
  - chain-selector: 3478487238524512106
    url: <Your RPC endpoint to Arbitrum Sepolia>
  - chain-selector: 16015286601757825753
    url: <Your RPC endpoint to ETH Sepolia>
```
Ensure the provided URLs point to valid RPC endpoints for the specified chains. You may use public RPC providers or set up your own node.

If you need to specify a custom settings file, use the `-S` or `--cli-settings-file` flag with any command:
```bash
cre workflow compile main.go -S /path/to/workflow.yaml
```

### Compile the Workflow
Run the following command to compile the workflow:
```bash
cre workflow compile main.go
```
Copy the generated Gist URLs for the binary and config artifacts, as you will need them later:
```bash
Binary gist created Gist URL=https://gist.githubusercontent.com/username/.../binary.wasm.br
```

### Workflow Deployment
Use the generated Gist URLs to deploy the workflow:
```bash
cre workflow deploy simple-workflow --binary-url=<binary-gist-url>
```
Ensure to save the workflow ID for tracking execution state in Grafana:
```bash
Workflow successfully registered Contract Address=0x... Transaction=0x... Workflow ID=...
```
