// Package standalone bootstraps single-binary CRE processes.
//
// Flag/env/config-file binding is handled by chainlink-common's pkg/config/flags: a
// dependency declares a tagged config struct and registers it with
// flags.RegisterCommandFlags(cmd, &cfg, "CRE", "CL") from AddCommands, which binds each field
// as a CLI flag, a viper key, and CRE_*/CL_* env vars, then decodes and validates it (see the
// `validate:"..."` tags) before the command runs.
//
// A binary gets two ways to run, as subcommands of its own root command:
//
//   - run: one instance, using the transports and stores it is configured with. What a deployed
//     binary does, and what the root command does with no subcommand at all.
//   - embed: --instances copies of the same thing inside one process, for running a DON on one
//     machine without the setup a DON of real nodes needs.
//
// Embedding is arranged entirely by the dependencies. A service is written once and knows nothing
// about it: everything that cannot be shared between instances is resolved through a
// BootstrapDependency, and each instance is handed the dependency that serves it (ForEmbedding)
// before its services are built. So a listening port becomes the next port along, a database
// becomes a schema of its own, and a peer's identity and transport become a derived key and an
// in-process channel - none of which the service serving on that port, or reading that database,
// has to be told about.
//
// Nothing downstream of that has an instance to reason about: an embedded dependency is already
// specific to its instance when it is built, so neither the services (StandaloneConfig) nor the
// dependencies themselves (CommonConfig) are told which one they belong to.
package standalone
