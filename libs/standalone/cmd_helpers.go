// Package standalone bootstraps single-binary CRE processes.
//
// Flag/env/config-file binding is handled by chainlink-common's pkg/config/flags: a
// dependency declares a tagged config struct and registers it with
// flags.RegisterCommandFlags(cmd, &cfg, "CRE", "CL") from AddCommands, which binds each field
// as a CLI flag, a viper key, and CRE_*/CL_* env vars, then decodes and validates it (see the
// `validate:"..."` tags) before the command runs.
package standalone
