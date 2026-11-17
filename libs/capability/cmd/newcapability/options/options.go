package options

type Options struct {
	// Category is the directory the capability goes in, such as scheduler. Prompted for when unset.
	Category string

	// Name is the capability's name, such as cron. Prompted for when unset.
	Name string

	// Trigger scaffolds a trigger. Required without Action.
	Trigger bool `validate:"required_without=Action"`

	// Action scaffolds an action.
	Action bool

	// Node scaffolds a capability in node mode.
	Node bool

	// OutDir is the directory the category directory is created in. Unset is the root of the repo libs is in.
	OutDir string

	// Force replaces the capability if it already exists. Only what newcapability scaffolded and generated is
	// replaced, so files added since are kept.
	Force bool

	// NoModule scaffolds the capability without a go.mod or project.json of its own.
	NoModule bool
}
