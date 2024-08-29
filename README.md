# Baku Capabilities

This repo uses [`nx`](https://nx.dev/) for monorepo management and change-detection.

## Code structure

- Each package in the `capabilities` folder creates a binary that instantiates a **capability set** when added to the node through a `type="standardcapabilities"` job spec (**capability spec**). A capability set contains one or more capabilities that are centered around some functionality or shared resource, e.g., KV store, EVM chain, CRON, etc. So a KV store binary would instantiate `kvstore-read` action capability and `kvstore-write` target capability that shares an underlying KV store resource.
- `libs` folder contains packages that are shared across capabilities. You should only create a package there if two or more capability sets need to share a dependency.

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
