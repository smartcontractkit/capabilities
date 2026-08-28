# `capability` — standalone capability binary, spike

## What this is

An in-progress replacement for `libs/standalone`'s bootstrapper. The goal is that a capability
binary's `main` is one line:

```go
func main() { capability.Run(ctx, trigger.NewCron) }
```

Everything a hand-written `main` does today — registering flags, resolving dependencies, handing
them to a factory — is a restatement of the capability constructor's parameter list. `Run` reads
that list by reflection and builds what it asks for.

The reference implementation to compare against is `libs/standalone/bootstrapper.go` (process
lifecycle) and `libs/standalone/capability` (capability hosting). Neither has been changed except
for one extraction (see *Relationship to `libs/standalone`*).

`cron/main.go` is the first user: `capability.Run(ctx, trigger.NewCron, capability.WithOtelViews(...))`,
with `trigger.Config` embedding `capability.Config`. The old bootstrapper remains for everything
else.

## Current state

`RunErr(ctx, lggr, ctor, opts...)` builds a cobra root (named after the executable), registers the
config sections on it, and hangs `run` and `embed` subcommands off it. `embed` is a stub.

`run` (in `runner.go`) is the whole sequence, in order:

```
profiler        (nil when no pyroscope server configured)
telemetry       building it installs the process-global beholder client
registry        grpc.NewClient (lazy) + registry.Local[.WithRemote]; Add serves+announces
settings        CRE settings from the dumped file, for the limits factory
constructor     newCapability.call(Dependencies{...})
reg.Add         serve the capability on its own gRPC server, hold locally, announce to the node
health checker  registers the services added above it
web server      /metrics, /debug/pprof, /healthz, /readyz, /reload/settings.txt,
                /debug/capabilities when --capabilities.http-debug
→ root.start(ctx)
→ block: plugin.Serve under a go-plugin host, else <-ctx.Done()
→ defer supervised.Close()
```

All of it is one `rootService` (`root.go`) — a `services.Engine` whose sub-services start in the
order added and close concurrently. `run` starts one thing and closes one thing.

Config is `config` (`config.go`) with `observability`, `capabilities` and `grpc` as siblings. Every
setting
is optional; `--http.port` defaults to 8080. A constructor may also declare one config struct of
its own by embedding `Config`; `bindConfig` registers it on the root under the binary's name
(`--cron.fastest-schedule-interval-seconds`, `CRE_CRON_...`) and `call` hands it over decoded.

Dependency injection: `constructor.call` (`run.go`) matches constructor parameters to
`Dependencies` fields **by type, by assignability, one field at a time** — except the `Config`
parameter, which comes from the flags. Currently `Logger`, `CapabilityRegistry` and
`LimitsFactory`.

## The list

Numbered against `libs/standalone/bootstrapper.go` unless noted.

### Done
1. Block until interrupted — `signal.NotifyContext` in `RunErr`, `<-ctx.Done()` in `run`
2. `plugin.Serve` under a go-plugin host — `underPluginHost()` + empty LOOP
3. `logger.Sync()` on shutdown — deferred first so it unwinds last
4. Logger named after the binary
6. `WithOtelViews` — `Option` on `Run`/`RunErr`
7. Start the capability — sub-service of the root
8. Health checker registration — registers its *siblings*, not the parent
16. Serve each capability on its own gRPC server — `server.go` + `registryService.Add`.
    `server.go` is a copy of `libs/standalone/grpc` minus the configured single-server form
    (crecore's), with one fix: the stop happens before the engine handoff, not in the `Close`
    hook, because the engine waits for the `Serve` goroutine *before* running the hook — the old
    arrangement deadlocks a started server's close.
17. Register and announce to the node's registry, remove on shutdown — `registryService.Add` and
    `close`. The registry owns serving itself: `Add` binds the server, holds the value, and calls
    `AddAt` with the address in scope, so the `addresses`-map convention `WithRemote` expects is
    deliberately nil'd out. Announcing happens at build time (right after the constructor), so a
    failed announce fails the run before it is nominally up and ready implies announced. `close`
    is one ordered function — Remove, stop servers, close conn — and uses a fresh context, not the
    engine's: `Close` closes the `StopChan` the engine's derives from before running the hook, so
    the old `eng.NewCtx()` deregistration was cancelled before it left the process (latent in
    `libs/standalone/capability` too).

### Skipped
5. `commonConfig` — an empty struct upstream; not needed yet

### Open — bootstrapper
9. Expose the run to constructors: the mux, metrics registerer, beholder client. The mux is the
   run's own (created early in `run`, so the reload endpoint registers on it), but constructors
   still have no way to reach it.
10. Resolve dependencies before calling the constructor — `bootstrap_gen.go` `Run1`…`Run10`
11. Close resolved dependencies in reverse — `registerCloser` (`:150`)
12. Register each dependency's config on the command that resolves it —
    `setupCommands`/`collectTargets`/`configSet` (`:374-479`). Each config instance must be bound
    exactly once or viper reads the wrong flag.

### Open — embed
13. `embed` command + `--instances` (stub at `run.go`)
14. `ForEmbedding` — per-instance dependency forms
15. Per-instance identity: logger `instance.N`, prometheus `instance` label (`:342-348`),
    `portFor(index)`, and an index on the root service name or health metrics collide

### Done — capability hosting
18. Settings reload endpoint — `reloadHandler` in `settings.go`, registered on the run's mux in
    `run`. The node dumps the file and hits `/reload/settings.txt`; 200 means every limit now
    resolves against the new payload, 500 means the previous settings are still in force.
19. Debug UI — `mountDebugUI` in `debugui.go`, under `/debug/capabilities`, gated on
    `--capabilities.http-debug` (off by default: it invokes capabilities). Fleet and hub are one
    each; they are the shared forms an embed run fans out over, so `embed` takes them over rather
    than inventing its own.

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
Registering the parent while the parent is still `Starting` would leave `/readyz` wrong for up to
15 seconds. It registers its siblings instead — `rootService.reporters()` snapshots what was added
before it, which is exactly what is already running when it starts.

**Constructors ask for individual dependencies, never the `Dependencies` struct.** Taking the struct
would be a dependency on everything a run has, and adding a field would silently widen what every
capability appears to need. `offered()` enforces this by not offering the struct.

**Matching is by assignability.** A capability can ask for `core.CapabilitiesRegistry` rather than
the wider `registry.Registry` the run holds. `provide()` reflects on the *dynamic* type, so a field
declared as a narrow interface still matches wider ones.

**The registry serves what it registers.** `registryService.Add` binds the server, mounts the
capability, holds the value locally, and announces with `AddAt` — one function, so serve-first-
announce-last is control flow rather than convention, and the address never leaves the scope that
made it. The `addresses` map `WithRemote` announces from is nil'd out: a shared write between
whoever serves and whoever announces is exactly the coupling this removes.

**The capability is announced before it is started.** `Add` runs at build time, right after the
constructor, so a failed announce fails the run before it is nominally up and `/readyz` implies
announced. The capability's own `Start` runs later, inside `root.start`; an RPC arriving in that
window would hit an unstarted capability, but the window is the rest of the build and the node
only learns the address at the end of it.

**`grpc.NewClient` does not dial, and does not validate.** The node starts this process and the two
race, so an eager dial would be a race we lose intermittently. It also accepts `""`, `"!://x"` and
unknown schemes without error — so a typo in `--capabilities.proxy-url` surfaces as a failed lookup
much later, not at startup.

**Sub-services start in order and close concurrently** (`services/service.go:254,281` —
`MultiStart` then `MultiCloser`). Start order is controlled by `rootService.add` order; close order
is *not* controlled at all.

**`StopOnce` refuses to run a `Close` hook on a service that never started**
(`services/state.go:111`, `ErrCannotStopUnstarted`). This is why anything that changes process state
at build time cannot rely on `Close` alone to undo it.

## Known gaps and defects

- **Telemetry global leaks on a pre-start failure.** The beholder global is swapped when telemetry
  is *built*, but the undo lives in the service's `Close`, which `StopOnce` refuses before the root
  has started it. A failure between building telemetry and `root.start` — the constructor,
  `newSettings`, the `reg.Add` that serves and announces, or `newHealthChecker` — leaves the global
  pointing at a client nothing will close. Accepted and documented inline in `run`, because `Run`
  exits the process straight after. **It must be fixed before `embed`**, where one failed instance
  would strand the global for the others.
  Note `TestRunUnwindsWhatStartedBeforeAFailure` passes *vacuously* here — telemetry is off in it.
- **A failure after `reg.Add` leaks the announcement too.** Same mechanism as the telemetry gap:
  the server is bound and the node's registry told the address at build time, but the undo lives in
  the registry's `Close`, which never runs if `root.start` is never reached. The node drops what it
  cannot dial, so the stale entry is self-healing; the bound port lasts until process exit. Fix
  alongside the telemetry gap, before `embed`.
- **`embed` must not serve the plugin host.** `run` decides for itself via `underPluginHost()`, so
  every instance would try. A host supervises one plugin. `TODO` on `run`.
- **Close ordering.** The registry's own close is now internally ordered (Remove → stop servers →
  close conn), so the deregistration reliably reaches the node. What remains: the registry and the
  capability are still closed concurrently by the root, so a draining RPC (servers stop with
  `GracefulStop`) can call into a capability that is already closing. The bootstrapper closed
  dependencies strictly after services (`registerCloser`). Unresolved.
- **Root service and logger share the binary's name.** Sub-services log as bare `Telemetry` /
  `HealthChecker` / `WebServer` with no binary prefix, and the root's health metric is labelled with
  the binary name. The bootstrapper used `Bootstrap` for the service to avoid this. Settle before 15.
- **Settings constants are a copy.** `settingsDirName`, `settingsFileName`, `reloadPathPrefix` in
  `settings.go` duplicate `libs/standalone/capability/settings.go` because importing it would be a
  cycle. They are a live contract with the node, which writes the file this reads. If they drift,
  settings silently stop arriving and every limit falls back to its compiled-in default with no
  error. `TestSettingsPathIsTheSharedConvention` guards this side only.
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
