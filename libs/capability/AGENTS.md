# `capability` — standalone capability binary, spike

## What this is

An in-progress replacement for `libs/standalone`'s bootstrapper. The goal is that a capability
binary's `main` is one line:

```go
func main() {
	capability.Run(ctx, trigger.NewCron, capability.NewConfigProvider(trigger.DefaultConfig),
		capability.WithOtelViews(trigger.MetricViews()...))
}
```

Dependencies register themselves, and their config, from their packages' `init`:
`capability.Provide[T](provider)` and `capability.ProvideConfig[T](namespace, default, opts...)`,
or their `ForRun`/`ForEmbed` forms to register for one command only (the plain forms call both). A
`Constructor` is `func(do.Injector) (Capability, error)`: it resolves what it needs, with
`do.Invoke[T](i)`, from the injector of the instance it is built for. The capability's own config is
a `ConfigProvider[T]` - its default and any `cli.RegisterOption[T]`s (a profile, say) - which
`RunErr` registers with `ProvideConfig` under the binary's name (`--cron.<field>`). A capability
with no config passes `NewConfigProvider(struct{}{})`. `Option` (`WithOtelViews`) is not generic.

The reference implementation to compare against is `libs/standalone/bootstrapper.go` (process
lifecycle) and `libs/standalone/capability` (capability hosting). Neither has been changed except
for one extraction (see *Relationship to `libs/standalone`*).

`cron/main.go` is meant to be the first user; it still runs on `loopserver` today. The old
bootstrapper remains for everything else.

## Current state

`RunErr(ctx, lggr, ctor, config, opts...)` builds a cobra root (named after the executable) and
hangs `run`, `embed` and `docs` subcommands off it, binding the configs registered for each
command on it. The constructor builds one capability per
instance. `embed` runs the same sequence as `run`, `--instances` times over the process-wide
services (profiler, telemetry, CRE settings).
`docs` generates the doc comments flag help is read from (see *Config*).

`start(ctx, lggr, name, opts, r, binder, total, ctor)` (in `runner.go`) is the whole sequence, the
same for both commands: `run` passes its registrations and 1, `embed` its own and `--instances`.
`start` never asks which:

```
-- once per process --
profiler        (nil when no pyroscope server configured)
telemetry       building it installs the process-global beholder client
settings        CRE settings, pushed by the host over gRPC when --settings.url is set
-- startInstance, for each ID in 0..total-1, on a fresh injector built from r --
logger          instance.N when Total > 1
registry        local registry, plus the node's (remote listener transport) when --capabilities.proxy-url is set
provide         Instance{id, n}, logger, registry, limits factory, clock, nil org resolver, binder
startCapability ctor(instance) → Start → reg.Add (local, then announced to the node's)
health checker  reports on this instance's services
web server      on http.port + ID: /metrics, /debug/pprof, /healthz, /readyz
-- end --
→ return stop: MultiCloser(svcs), concurrent — close order is not controlled
```

The commands do the waiting: `run` serves the go-plugin host if one launched it, else waits for
ctx; `embed` only waits (a host supervises one plugin, and an embed run is not one).

`run` keeps a plain `[]services.Service`: each piece is **started as it is built** (`startX`
wrappers over `newX`) and appended, and a deferred `services.MultiCloser` at the top closes
whatever was appended — so a failure at any step unwinds everything before it. There is no
aggregate root service.

Config is any struct registered with `ProvideConfig` (`provide.go`), which wraps it into a function
that binds it on a command with the `cli.Binder`, and a provider that reads what the flags decoded
into it. This package's own sections (`observability`'s, `capabilities`, `grpc`, `settings`, and
embed's `--instances`) are registered by `registerSettings` - as a dependency registers its own,
but from `RunErr` rather than `init`. It therefore runs after every dependency's `init`, so a
dependency registering one of those types is a duplicate and fails the run rather than overriding
it. They bind with `chainlink-common/x/config/cli` (flags, `CRE_`/`CL_` env vars, repeatable
`--config` TOML files). `start` resolves its own settings (`resolveSettings`). Every setting is
optional; `--http.port` defaults to 8080.

The decode fills the registered struct in place, and the provider reads it on resolution, so a
config must not be resolved before the flags decode - which nothing does, since `init` only
registers. Registrations are process-wide, so `RunErr` runs once per process; tests swap in fresh
ones with `resetRegistrations`.

Flag help text is the field's **doc comment**, not a tag: `cli` reads it from a generated
`DocComments()` method. Every binary gets a `docs` subcommand (`docs.go`) that runs
`commentparsing.Run` over every section bound on it and writes `doccomments_gen.go` beside each
struct in the module enclosing the working directory, so a capability documents its config with
`//go:generate go run . docs` instead of a `gen/main.go` of its own. Structs from other modules are
read from their compiled `DocComments`, which is why this package generates its own via
`internal/docgen` (`go generate` here). `TestEveryFlagIsDocumented` catches a stale file. The
`Tool` header is per binary name, because `commentparsing.Run` deletes files its tool wrote but
did not produce this time.

Dependency injection is `samber/do`, keyed **by exact type**. There are no containers to derive:
each command has a list of registrations (`provide.go`), and every instance gets a fresh injector
built from its command's list, onto which the run provides its own values:

```
run registrations       Provide, ProvideForRun, ProvideConfig, ProvideConfigForRun (init / RunErr)
└── instance 0          fresh injector + Instance{0, 1}, logger, registry, limits factory, clock, binder; ctor
embed registrations     Provide, ProvideForEmbed, ProvideConfig, ProvideConfigForEmbed
├── instance 0          fresh injector + Instance{0, n}, ...; ctor
└── instance n-1        fresh injector + Instance{n-1, n}, ...
```

**Embed-specific settings**, following `rtinianov_configWithOwner`'s `libs/standalone`: embed
registers its own form of a run setting under the same namespace, with the `ForEmbed` functions,
while run registers run's with the `ForRun` ones. Today that is `capabilities` → `embeddedCapabilityConfig` (`embed.go`): no
`proxy-url` (no node to dial, so only what this binary hosts resolves, nothing is announced) and
`capability-don-id` defaulting to 1; embed provides `capabilityConfig` built from it. Each instance has its own logger (`instance.N` when there are several), registry,
health checker and web server on `http.port + ID`, as that branch's `portFor(index)`; an
embed run never serves a go-plugin host. Not carried over: per-instance prometheus labels (no
per-instance registerer yet), and the debug UI being forced on (removed). `docs` walks both commands' registrations, since each
holds configs the other does not; `TestEveryConfigIsDocumented` guards that.

The run provides its values onto each instance's injector, so a dependency registered in `init`
resolves against that instance's, and each instance builds its own dependencies. A type a binary
registers that the run also provides is refused (`provideValue`): the run's values are not
replaceable. `Instance` (`instance.go`) is provided to every instance: `{0, 1}` under run. The
`*cli.Binder` is provided too.
The registry is provided as `registry.CapabilitiesRegistry`, which `core.CapabilitiesRegistry` is
an alias of; the clock as `clockwork.Clock` (real), so a test can shadow it.

## The list

Numbered against `libs/standalone/bootstrapper.go` unless noted.

### Done
1. Block until interrupted — `signal.NotifyContext` in `RunErr`, `<-ctx.Done()` in `run`
2. `plugin.Serve` under a go-plugin host — `underPluginHost()` + empty LOOP
3. `logger.Sync()` on shutdown — deferred first so it unwinds last
4. Logger named after the binary
6. `WithOtelViews` — `Option` on `Run`/`RunErr`
7. Start the capability — an entry in the run's service list
8. Health checker registration — registers its *siblings*, not the parent
16. Serve each capability to the node — `registry.go`, on chainlink-common's `registry` and
    `registry/remote` packages: the remote client serves what `Add` is handed over its own
    listener transport (bound on `--grpc.advertise-host`), so this package opens no gRPC servers
    of its own.
17. Register and announce to the node's registry, remove on shutdown — `registryService.Add` and
    `close`. `Add` registers locally, then with the node's, rolling the local back if the node
    rejects it, so the two never disagree. `close` removes everything added, in reverse, with a
    fresh context rather than the engine's: `Close` closes the `StopChan` the engine's derives
    from before running the hook, so the deregistration would be cancelled before it left the
    process.

### Skipped
5. `commonConfig` — an empty struct upstream; not needed yet

### Open — bootstrapper
9. Expose the run to constructors: the mux, metrics registerer, beholder client. Each instance
   has its own mux, but constructors still have no way to reach it.
10. Resolve dependencies before calling the constructor — `bootstrap_gen.go` `Run1`…`Run10`
11. Close resolved dependencies in reverse — `registerCloser` (`:150`)
12. Register each dependency's config on the command that resolves it —
    `setupCommands`/`collectTargets`/`configSet` (`:374-479`). Each config instance must be bound
    exactly once. (`cli` now enforces the no-clash half: a key shared with an ancestor command fails
    the whole tree.)

### Open — embed
13. ~~`embed` `--instances`~~ — done; see the container tree above.
14. `ForEmbedding` — per-instance dependency forms
15. Per-instance identity: prometheus `instance` label (`:342-348`). Logger `instance.N` and
    `portFor` (`http.port + ID`) are done.
    Each instance has its own registry and health checker, so instances may share service names
    and capability IDs; telling them apart elsewhere (schemas, DON identities) is the
    dependency's job, from its `Instance`.

### Done — capability hosting
18. Settings pushed by the host — `settings.go`. With `--settings.url` set, the settings service
    subscribes to the host's CRE settings stream, applies every update, and resubscribes with
    backoff whenever the stream cannot be made or ends; a payload that cannot be applied leaves
    the previous settings in force. Unset, every limit stays at its compiled-in default.
19. ~~Debug UI~~ — removed along with `libs/standalone/protohelpers/ui`, and its
    `--capabilities.http-debug` flag with it.

## Decisions, and the constraints behind them

These are the non-obvious ones. Several were re-litigated more than once before the constraint was
found, so they are worth reading before changing the order of anything in `run`.

**Telemetry is installed at build time, not in `Start`.** A capability creates its OTEL instruments
while it is being *constructed* (`cron/trigger/metrics.go:47` calls `beholder.GetMeter()` inside
`NewMetrics`), and an instrument resolves its meter once. Installing later leaves every capability
metric bound to the noop meter for the life of the process, silently. Same reason the limits factory
is built after telemetry.

**The health checker cannot be inside the thing it reports on.** `Register` seeds state by calling
`reporter.Ready()` immediately and only re-reads on a **15s** tick (`services/health.go:63`).
Registering a still-starting aggregate would leave `/readyz` wrong for up to 15 seconds. It
registers its siblings instead — `run` hands it a snapshot of the slice built so far, which is
exactly what is already running when it starts.

**Capabilities resolve individual dependencies; there is no bag.** Resolving a struct of everything
would be a dependency on everything a run has, and adding a field would silently widen what every
capability appears to need.

**Matching is by exact type, not assignability.** `do` keys on `do.NameOf[T]`, so a narrower
interface only resolves if the run registers it under that type. Any narrowing a capability wants
has to be added in `startInstance`.

**Local first, then the node's.** Every lookup tries the capabilities this binary hosts before
the node's registry: a capability hosted here is a value this process already has, so resolving
it locally hands back the implementation rather than a gRPC client looping back into this same
process.

**The capability is started before it is announced.** `startCapability` runs the constructor,
starts the capability, and only then `reg.Add` serves and announces it — traffic the announcement
invites lands on something already running. Announcing at build time, rather than in a service's
`Start` of its own, means a failed announce fails the run before it is nominally up, and `/readyz`
implies announced.

**`grpc.NewClient` does not dial, and does not validate.** The node starts this process and the two
race, so an eager dial would be a race we lose intermittently. It also accepts `""`, `"!://x"` and
unknown schemes without error — so a typo in `--capabilities.proxy-url` surfaces as a failed lookup
much later, not at startup.

**Services start eagerly, in build order, and close concurrently** (`services/multi.go` —
`MultiCloser`). `run` starts each service as it builds it (`startX` wrappers) and appends it to
the slice; the deferred `MultiCloser` at the top of `run` closes whatever was appended, which is
what makes a failure at any step unwind everything before it. Close order is *not* controlled.

**`StopOnce` refuses to run a `Close` hook on a service that never started**
(`services/state.go:111`, `ErrCannotStopUnstarted`). This is why anything that changes process state
at build time cannot rely on `Close` alone to undo it.

## Known gaps and defects

- **~~Telemetry global leaks on a pre-start failure~~ — resolved.** Every service is started as it
  is built and appended to `svcs`, and the deferred `MultiCloser` at the top of `run` closes
  whatever was appended, so a failure at any later step — the constructor, `newSettings`,
  `reg.Add` — unwinds telemetry's global swap along with everything else.
- **~~A failure after `reg.Add` leaks the announcement~~ — resolved**, same mechanism: the
  registry is started (so its `Close` runs) and on the slice before `startCapability` is called.
- **The injectors are never shut down.** Anything a provider builds that
  implements `do.Shutdowner` is not shut down when the run ends. Wiring `Shutdown` in needs an order
  relative to the `MultiCloser` (after it, so the capability is closed before its dependencies);
  not done.
- **Close ordering.** The registry's own close is internally ordered (Remove each added, then
  close the connection), so the deregistration reliably reaches the node. What remains: the
  registry and the capability are still closed concurrently (`MultiCloser`), so an in-flight RPC
  can call into a capability that is already closing. The bootstrapper closed dependencies
  strictly after services (`registerCloser`). Unresolved.
- **The empty LOOP is a local copy** (`plugin.go`). The chainlink-common this pins no longer ships
  `loop.EmptyLoop`; the handshake cookie has to keep matching the node's, which launches this
  binary with it.
- **`CL_PROMETHEUS_PORT` is ignored.** Under a go-plugin host the node assigns a metrics port via
  that env var, but this binds `CRE_HTTP_PORT`/`CL_HTTP_PORT`. Pre-existing — the bootstrapper has
  the same naming — but now quieter, since the 8080 default means the binary starts anyway.
- **`loop.TracingConfig.OnDialError` is still nil in `libs/standalone/telemetry.go`.** Fixed here
  (`telemetry.go`, `beholderConfig`); still live in the shipping bootstrapper, where
  `--tracing.enabled` plus an unreachable collector is a nil dereference on a background goroutine
  that kills the process.

## Relationship to `libs/standalone`

This package is a deliberate **copy** of the observability config and helpers, not a move.
`libs/standalone` imports this package for `NewLogger`, so this package cannot import it back
without a cycle. The duplicate is meant to resolve by *deletion* — when this is what starts a
binary, the ones in `standalone` go.

The only change made to `libs/standalone` is that `newLogger` was moved out of `telemetry.go` into
this package's `logger.go`, and `bootstrapper.go` now calls `capability.NewLogger()`.

## Running the tests

```
cd libs
go test ./capability/ -short              # ~1s
go test ./capability/ -short -race
go test ./capability/                     # ~21s
```

**Use `-short` by default.** One test —
`TestRunInstallsTelemetryBeforeBuildingTheCapability` — builds a real beholder client, and closing
one flushes to a collector that is not there: about 20s of export timeouts. It is gated on
`testing.Short()`. It is also the only test that would catch the silent-noop-metrics regression
described above, so do not delete it.

Tests bind real TCP ports (`freePort`) and mutate `os.Args` and the beholder global, so they are not
parallel-safe. `execute()` in `run_test.go` drives the real entry point and stops the run by
cancelling its context once the HTTP server answers.

## Style

Comments explain *why*, not what — particularly the ordering constraints above, which are otherwise
invisible and have been reintroduced by accident more than once. Match the surrounding density.
