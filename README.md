# Baku Capabilities

This repo uses [`nx`](https://nx.dev/) for monorepo management and change-detection.

## Code structure

- Each package in the root folder creates (except `libs`) a binary that instantiates a **capability set** when added to the node through a `type="standardcapabilities"` job spec (**capability spec**). A capability set contains one or more capabilities that are centered around some functionality or shared resource, e.g., KV store, EVM chain, CRON, etc. So a KV store binary would instantiate `kvstore-read` action capability and `kvstore-write` target capability that shares an underlying KV store resource.
- `libs` folder contains packages that are shared across capabilities. You should only create a package there if two or more capability sets need to share a dependency.

## Generating SDKs

Each capability set should have a sub-folder that defines a JSON schema for each function call users can make.  
They may also contain a JSON schema for common definitions that many function calls use.

Having a sub-folder allows the packages to live in isolation from the capabilities themselves.  
This is important for keeping WASMs small and only including the code that is needed.  
If the JSON schema is itself not generated from code, then the capabilities should make use of these types.

### gen file

There must be a file to generate the types.  
It's recommended to put it in the JSON schema's directory so it's all together, but it will also work from the root of the capability.

The latter may be useful if you have multiple versions of the capability, as one file can be used for all generations.

See `cron/croncap/gen.go` for an example.

If a capability is tightly coupled to another (e.g., targets with consensus), it is okay to refer to their JSON schema for a specific type.

**Note**: If you refer to URLs for additional schemas, you will need to add them to the run command in `gen.go`.  
See `kvstore/kvcap/gen.go` for an example.

### File naming

The files must follow the regex [CapabilitySchemaFilePattern](https://github.com/smartcontractkit/chainlink-common/blob/main/pkg/capabilities/cli/cmd/generate_types.go#L21).  
If you expose one function to users, your schema should follow:

`name_type-schema.json`, like `cron_trigger-schema.json`.

The method on the config file will then be named `Config` that users interact with.

If you have multiple functions, you can name it first with your capability, then the function name.

For example:

`kv_write_target-schema.json`

This will generate:

`WriteTargetConfig` for users to interact with, allowing for:

`kv_read_action-schema.json`, `kv_batch_read_action-schema.json`, etc., to generate more types in the same namespace.

Common methods may live in:

`capability_common-schema.json`

`kv_common-schema.json`

### JSON schema requirements

All schemas must have a $id, the id must match the package that the folder it is in will resolve to, 
followed by either the capability's name and version or an interface name. 

For example, the cron trigger is
```"$id": "https://github.com/smartcontractkit/capabilities/cron/croncap/cron-trigger@1.0.0",```

whereas the common type for a chain reader would be similar to

```"$id": "https://github.com/smartcontractkit/capabilities/chain/chaincap/reader",```

The prior will not require a user to specify which capability to bind to at runtime, whereas the latter will.

#### Triggers

Triggers must contain an output and config types.  
If you wish for the output to have a better name than the default, create it in `$defs` and refer to that.

#### Actions

Actions must contain input, output, and config types.  
If you wish for the input and output to have a better name than the default, create them in `$defs` and refer to those.

#### Targets

**Note**: At the time of writing this, it has been decided that targets will likely go away in favour of becoming actions.  
Until that happens, targets must contain input and config types.

#### Common

Common schemas only generate helper types for capabilities and should only be used for their `$defs` section.


## Design Principles

### ❌ Capabilities should not reference other capabilities

Each binary can pull the dependencies it needs without adding them to all the other binaries. If some shared dependencies emerge, e.g., input and output types, then those should be extracted to a separate lib in the `libs` folder and referenced from there (`project.json` files should be updated accordingly).

This allows capabilities to evolve independently of each other. It is encouraged to have a separate README.md file for each capability.

### ❌ No imports from `chainlink` repo

There should be no imports from the `chainlink` repository which hosts the node, because this creates a risk for circular dependencies.

## Dependencies

For dependency graph run:

```sh
./nx dep-graph
```

`capabilities` repo also depends on the platform (`chainlink`) repo. The
platform defines and operates on the interfaces.

## Tasks

Run a task for all projects:

```sh
./nx run-many -t test
```

Run a task for the affected projects only:

```sh
./nx affected -t test
```

[More info on Nx Tasks](https://nx.dev/features/run-tasks).
