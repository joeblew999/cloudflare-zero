package e2e

import (
	"testing"

	misecheck "github.com/joeblew999/mise-check"
)

// TestMise is misecheck (github.com/joeblew999/mise-check) on a mise file of this repo and the Go it runs, both ways.
// The task that runs it says which by its env, as mise run root:check-mise and template:check-mise do.
func TestMise(t *testing.T) { misecheck.Check(t, misecheck.FromEnv()) }
