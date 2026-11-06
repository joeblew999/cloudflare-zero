// The root's tool: task that isn't a one-line command: mise run tool:release. It runs only from its own task
// (MISE_TASK_NAME), so go test ./... never releases.

package e2e

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/joeblew999/mise-check/walk"
)

// TestToolRelease: mise run tool:release releases mise-cloudflare from the commit checked out (release.yml runs the
// same on GitHub): the commit's v* tag, or the next patch version, tagged and pushed, when it has none; goreleaser's
// binaries for every machine on that tag's GitHub release (.goreleaser.yaml); a new repo pinning the release, tried as a
// person tries it (mise run new-repo:e2e-serve --version: the walk on a copy stamped out pinning it); and the version fetched by
// proxy.golang.org, so go get and pkg.go.dev have it at once. With --dry-run (the test's flag -dry-run) it tags and
// publishes nothing: goreleaser builds a snapshot into dist/.
func TestToolRelease(t *testing.T) {
	walk.OnlyByName(t)
	tag, isNew, err := releaseTag()
	if err != nil {
		t.Fatal(err)
	}
	if *dryRunFlag {
		fmt.Printf("· would release %s%s\n", tag, map[bool]string{true: " (a new tag on this commit)", false: " (this commit's tag)"}[isNew])
		runShown(t, nil, "goreleaser", "release", "--snapshot", "--clean")
		fmt.Println("· dry run: nothing tagged, nothing published")
		return
	}
	if isNew {
		identity := []string{}
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			identity = []string{"-c", "user.name=github-actions", "-c", "user.email=github-actions@users.noreply.github.com"}
		}
		runShown(t, nil, "git", append(identity, "tag", "-a", tag, "-m", tag)...)
		runShown(t, nil, "git", "push", "-q", "origin", tag)
		fmt.Println("tagged", tag)
	}
	runShown(t, []string{"GORELEASER_CURRENT_TAG=" + tag}, "goreleaser", "release", "--clean")
	runShown(t, nil, "mise", "run", "new-repo:e2e-serve", "--version", tag)
	out, err := exec.Command("go", "list", "-m").Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	runShown(t, []string{"GOPROXY=https://proxy.golang.org", "GOFLAGS=-mod=mod"}, "go", "list", "-m", strings.TrimSpace(string(out))+"@"+tag)
}

// dryRunFlag is mise run tool:release --dry-run, passed as the test's flag (go test … -args -dry-run).
var dryRunFlag = flag.Bool("dry-run", false, "tag nothing and publish nothing: goreleaser builds a snapshot into dist/")

// releaseTag is the tag to release: the commit's own v* tag, or the next patch version after the newest v* tag (v0.0.1
// for the first), which is new.
func releaseTag() (tag string, isNew bool, err error) {
	if out, err := exec.Command("git", "describe", "--tags", "--exact-match", "--match", "v*").Output(); err == nil {
		return strings.TrimSpace(string(out)), false, nil
	}
	out, err := exec.Command("git", "tag", "-l", "v*").Output()
	if err != nil {
		return "", false, fmt.Errorf("git tag -l: %w", err)
	}
	newest := [3]int{}
	for _, t := range strings.Fields(string(out)) {
		if v, ok := parseTag(t); ok && lessTag(newest, v) {
			newest = v
		}
	}
	return fmt.Sprintf("v%d.%d.%d", newest[0], newest[1], newest[2]+1), true, nil
}

// parseTag reads vMAJOR.MINOR.PATCH; anything else isn't a release tag.
func parseTag(tag string) ([3]int, bool) {
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if !strings.HasPrefix(tag, "v") || len(parts) != 3 {
		return [3]int{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

func lessTag(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// runShown runs a command with env added to this one's, its output shown, and stops the test at its failure.
func runShown(t *testing.T, env []string, name string, args ...string) {
	t.Helper()
	fmt.Println("$", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
