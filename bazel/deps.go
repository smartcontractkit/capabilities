//go:build bazel
// +build bazel

// This file ensures golang.org/x/tools is a direct dependency for Bazel builds.
// It's only compiled when building with Bazel (using the "bazel" build tag),
// so it doesn't affect normal Go builds.
package bazel

import (
	_ "golang.org/x/tools/go/analysis/passes/appends" // required for bazel nogo
)
