# Baku Capabilities

This repo uses [`nx`](https://nx.dev/) to easy monorepo management and implement
change-detection.

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
