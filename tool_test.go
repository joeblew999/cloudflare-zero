// The root's tool: task that isn't a one-line command: mise run tool:release. It runs only from its own task, so go
// test ./... never releases.

package e2e

import (
	"testing"

	"github.com/joeblew999/mise-check/release"
)

// TestToolRelease: mise run tool:release releases mise-cloudflare from the commit checked out, by mise-check's release
// (release.yml runs the same on GitHub): the commit's v* tag, or the next patch version, tagged and pushed; goreleaser's
// binaries on that tag's GitHub release (.goreleaser.yaml); a new repo pinning the release, walked as far as serve
// (new-repo:e2e-serve --version); and the version fetched by proxy.golang.org. With --dry-run it tags and publishes
// nothing: goreleaser builds a snapshot into dist/.
func TestToolRelease(t *testing.T) { release.Run(t, release.Config{NewRepo: "new-repo:e2e-serve"}) }
