// Package options is what newcapability is told to scaffold, in its own package so its flags' help text can be
// generated from these comments, by libs/capability's docgen.
package options

// Options is what newcapability scaffolds and where.
type Options struct {
	// Category is the directory the capability goes in, such as scheduler. Prompted for when unset.
	Category string

	// Name is the capability's name, such as cron. Prompted for when unset.
	Name string

	// Trigger scaffolds a trigger.
	Trigger bool

	// Action scaffolds an action; with Trigger, one capability that is both.
	Action bool

	// OutDir is the directory the category directory is created in. Unset is the root of the repo libs is in.
	OutDir string

	// Force replaces the capability if it already exists. Only what newcapability scaffolded and generated is
	// replaced, so files added since are kept.
	Force bool

	// NoModule scaffolds the capability as packages of the module it is in, without a go.mod or project.json of its
	// own.
	NoModule bool
}
