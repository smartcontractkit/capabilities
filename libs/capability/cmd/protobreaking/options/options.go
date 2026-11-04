// Package options is what protobreaking is told to compare, in its own package so its flags' help text can be
// generated from these comments, by libs/capability's docgen.
package options

// Options is what protobreaking compares.
type Options struct {
	// Against is a checkout of the baseline, usually main.
	Against string `validate:"required"`

	// Checkout is the checkout to check. Unset is the current directory.
	Checkout string
}
