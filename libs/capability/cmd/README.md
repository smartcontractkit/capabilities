# libs/capability/cmd

Code generation tools for capability modules. These are binaries, not libraries, but they live alongside the
`capability` package in the `libs` module on purpose:

- `protoc-gen-cre` generates code against the types in `libs/capability`. Keeping it in the same module means
  the generator and the runtime it targets are always at the same version, so generated code can't drift from
  the `libs` a capability compiles against.
- Every capability module already requires `libs`, so the tools resolve through the caller's existing
  `go.mod` with no extra `require` or version bump to keep in sync.

## Tools

- `protoc` generates the Go messages, capability servers and production protos for the `.proto` files in a
  module's `protos` directory. Run it from that directory:

  ```go
  //go:generate go run github.com/smartcontractkit/capabilities/libs/capability/cmd/protoc
  ```

- `protoc-gen-cre` is the protoc plugin `protoc` uses to generate a `capability.Capability` server for each
  proto service, and the production protos. It is not normally run directly. Both come from the one plugin, so
  a capability can't be generated without its production protos.

  A capability's `protos` are its development protos, and every field, method and enum value in them is tagged
  with the feature it belongs to, with the options in `capability/feature/feature.proto`. The capability's service
  lists every feature as released or in progress, and a feature is released whole by moving it from one list to the
  other, so tags never change once written:

  ```proto
  string message = 1 [(cre.feature.v1.field) = "timezones"];
  LEVEL_HIGH = 1 [(cre.feature.v1.value) = "timezones"];

  service Cron {
    option (cre.feature.v1.released) = "launch";
    option (cre.feature.v1.in_progress) = "timezones";

    rpc Execute(Request) returns (Reply) {
      option (cre.feature.v1.method) = "timezones";
    }
  }
  ```

  Every file has a production file with its services, their released methods, the messages with released fields,
  what those reach through released fields, the released values of the enums they use, and the imports those need,
  without the feature options. A message no method uses any more, such as one kept for a retired method, stays
  released through its released fields. Untagged elements are never released. Generation fails if anything is
  tagged with a feature neither list has, a listed feature tags nothing, or a feature is in both lists, so a
  misspelt name can't leave anything unreleased. It also fails for a released enum whose zero value isn't released. A file with
  nothing released still has one, with only its package and services, which is valid, so the production
  protos exist from a capability's first generation. No Go is generated from them: the SDKs generate them in
  the language they need, and choose their own `go_package`.

  The production protos are what the SDKs use, so their proto package never changes. The development package is
  told apart from it by an `x` before its version, which the production package drops:
  `capabilities.scheduler.cron.x.v1` is released as `capabilities.scheduler.cron.v1`. Production files are at
  their path in that package under `protos/production`, such as
  `protos/production/capabilities/scheduler/cron/v1/trigger.proto`, which is what they import each other by and
  are registered as, so `protos/production` is their include path and a binary can link both.

  Generated servers read every request with `feature.UnmarshalAny`, which takes either message into the
  development one and reports whether it was production, and reply with `server.Respond` and
  `server.ForwardTriggers`, which send the production message when the request was. Requests are `Any`
  payloads only: `values.Map` inputs and configs are no longer read.

  `production/testdata` holds protos and the production protos they generate.

- Every capability fetches the protos it imports, the base protos in chainlink-protos, with
  [protofetch](https://github.com/coralogix/protofetch), at the version the repo's `pnpm-lock.yaml` pins, which
  `pnpm install` installs. The `protofetch.toml` at the root of the repo pins the revision for every module, and
  `protofetch.lock` records the commit. `protoc` fetches them before generating and imports from what it fetched,
  `.deps`, which is not committed. Repos that fetch a capability's production protos get the base protos at that
  commit too, from the same `protofetch.toml`, so the two never diverge.

  The commit fetched must be the one each module's `go.mod` requires the generated Go of, `chainlink-protos/cre/go`,
  at, or it would generate against one and link the other, so `protoc` moves the older of the two to the newer
  before it fetches: `go.mod` with `go get`, or `protofetch.toml` and `protofetch.lock` with `protofetch update`. A
  module moving the base forward moves it for every module, which their next generate follows; changes to the root
  `protofetch.toml` and `protofetch.lock` make every project affected, so CI's generate check catches any that
  doesn't.

- `protobreaking` fails if a change breaks the production protos of any capability in the repo, with buf's `FILE`
  rules, against a checkout of main. They are checked together, as their paths come from their packages, so a
  capability can move without them changing. A new version is a new package, so adding one never breaks. A
  production package can only be retired whole, by adding it to `retired_protos.txt` at the root of the repo in
  the change that removes it. A retired package stays retired, so the file is only ever added to:

  ```
  # Moved to v2.
  capabilities.scheduler.cron.v1
  ```

- `newcapability` scaffolds a capability module at `<out-dir>/<category>/<name>` - protos, main, go.mod, config
  with its doc comments, and a test - then generates and tidies it. `--trigger` alone serves `trigger.proto`;
  `--action`, with or without `--trigger`, serves one `client.proto` service with both, as EVM's client does.
  `--out-dir` defaults to the repo root, and a missing `--category` or `--name` is prompted for. From `libs`:

  ```sh
  go run ./capability/cmd/newcapability --trigger --action --category scheduler --name cron
  ```

  Every field and method of the scaffolded proto is tagged with the `example` feature, so nothing is in
  production until it is retagged. The implementation is always in `service`, and `main` runs `service.New`. `main`'s `//go:generate go run .
  docgen` runs the hidden `docgen` command every `capability.Run` binary has, which generates the doc comments
  for every config the capability provides that its module declares. Its test shows how to drive it with
  `capabilitytest` and the generated `protostest` instances, checks that readiness and health follow Start and
  Close, and covers all of its hand-written code.

  `--force` scaffolds over an existing capability, replacing only what scaffolding wrote: the files from its
  templates and the generated ones, so files added since are kept. `--no-module` scaffolds it as packages of the
  module it is in, without its own `go.mod` or `project.json`; that module fetches what its
  protos import and generates its config's doc comments.

  `go generate ./internal/example` scaffolds `internal/example/capability` into `libs`, with `--no-module`, a
  trigger and an action, so a change to `libs` regenerates it with that change. It is committed, so CI's generate
  check fails if the templates or `protoc-gen-cre` change without it, and its tests are the tests of both.
  `internal/debugui` tests the debug UI serving it.
