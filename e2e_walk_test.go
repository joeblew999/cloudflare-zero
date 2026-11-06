// Package e2e is this repo's end-to-end test: a walk of a repo's numbered tasks, through the repo's own mise file, on
// mise-check's walk (github.com/joeblew999/mise-check/walk), which runs the steps in their order and does everything
// that isn't Cloudflare's. It sits at the top of this repo, beside the root mise.toml whose tasks run it, in a file for
// each thing it does:
//
//   - e2e_walk_test.go, this file: the walk tests the root's tasks run by name (which repo is the test's name; how
//     far, its flag -stage), and the stages.
//   - e2e_steps_test.go: the steps, the repo's numbered tasks in their order, each with its check.
//   - e2e_mise_test.go: the repo, and what's Cloudflare's in making one: a new repo (mise-cloudflare init, then
//     cloudflare:template), an adopted third-party Worker, a checkout of HEAD, and the calls the steps make.
//   - e2e_cloudflare_test.go: what the walk looks at besides, as a person does: the Worker's URL, the account's D1, KV
//     and R2, Cloudflare's builds.
//
// tool_test.go is the release (mise run tool:release), and mise_test.go checks that the mise files and the Go line up,
// both ways.
package e2e

import (
	"flag"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/joeblew999/mise-check/walk"
)

// cloudflareWalk is the walk: the template's numbered tasks (templates/.mise/tasks/mise-cloudflare.toml), in their
// order, each checked, as far as -stage. Which repo is the test's name: the template where it is, a new repo made the
// README's way (at the release -version names or this checkout's build), or a third-party Worker adopted as its owner
// would (-adopt).
var cloudflareWalk = walk.Walk[*walker]{Steps: steps, Stages: stageNames, New: newRepo, Prepare: prepare, Of: walkOf}

// The walks, each run only when its task names it (go test . -run "^TestWalkTemplate$" -args -stage serve), so go
// test ./... never touches Cloudflare.

// TestWalkTemplate walks the template, templates/, where it is (mise run template:e2e-serve, …).
func TestWalkTemplate(t *testing.T) { cloudflareWalk.Template(t, "templates") }

// TestWalkNewRepo walks a new repo made the README's way (mise run new-repo:e2e-serve, new-repo:e2e-deploy).
func TestWalkNewRepo(t *testing.T) { cloudflareWalk.NewRepo(t) }

// TestWalkNewRepoExtras walks a new repo whose developer first adds the extras (mise run new-repo:e2e-extras).
func TestWalkNewRepoExtras(t *testing.T) {
	walk.OnlyByName(t)
	walkExtras = true
	cloudflareWalk.NewRepo(t)
}

// TestWalkNewRepoEnv walks a new repo whose developer adds a wrangler environment, env.staging, and deploys it (mise
// run new-repo:e2e-env).
func TestWalkNewRepoEnv(t *testing.T) {
	walk.OnlyByName(t)
	walkEnv = "staging"
	cloudflareWalk.NewRepo(t)
}

// TestWalkAdopt walks a third-party Worker of the corpus, its folder -adopt names, adopted as its owner would (mise
// run repos:e2e-adopt <path>).
func TestWalkAdopt(t *testing.T) {
	walk.OnlyByName(t)
	if *adoptFlag == "" {
		t.Fatal("-adopt names the Worker's folder in repos/: mise run repos:e2e-adopt <path> passes it")
	}
	cloudflareWalk.Template(t, "repos")
}

// walkExtras and walkEnv are what the walk adds to a new repo before its steps, as its test says: the extras, and a
// wrangler environment to deploy instead of the top level.
var (
	walkExtras bool
	walkEnv    string
)

// logoutFlag, the test's flag -logout, has 14-logout log out: without it the login is kept, so the next walk needs
// no Allow.
var logoutFlag = flag.Bool("logout", false, "log out at 14-logout (default keep the login, so the next walk needs no Allow)")

// TestSteps checks that the steps are the template's numbered tasks, in their order, so the walk is that file's walk.
func TestSteps(t *testing.T) {
	walk.CheckSteps(t, "templates/.mise/tasks/mise-cloudflare.toml", steps)
}

// adoptFlag is a third-party Worker's folder in repos/, as repos:check cloned it, to adopt and walk instead, the test's
// flag -adopt: mise run repos:e2e-adopt repos/cloudflare/realtime-examples/video-to-jpeg passes it.
var adoptFlag = flag.String("adopt", "", "a third-party Worker's folder in repos/ to adopt and walk (repos:e2e-adopt)")

// stage is how far a walk goes. Each runs the steps the stages before it run, and the steps that need it.
type stage int

const (
	serve         stage = iota // as far as 06-serve: the Worker locally, needing nothing, CI included
	deploy                     // as far as 08-deploy: deployed from here, needing a Cloudflare account (and an Allow, the first time)
	connectGithub              // as far as 09-connect-github: Cloudflare deploying every push, needing the repo on GitHub too
)

// stageNames are the stages as -stage names them: after the step each goes as far as.
var stageNames = []string{"serve", "deploy", "connect-github"}

func (s stage) String() string { return stageNames[s] }

// upTo is how far this walk goes, as -stage names it (the walk has checked it's one of stageNames).
func upTo() stage { return stage(max(0, slices.Index(stageNames, *walk.Stage))) }

// newRepo is a new repo, made the README's way: an empty folder with its own git, mise-cloudflare init at the release
// version names (this checkout's build without one), then cloudflare:template. It has its own git, not on GitHub,
// where connect-github's steps push, so it walks as far as deploy.
func newRepo(r walk.Repo, version string) walk.Repo {
	switch {
	case *adoptFlag != "":
		r.T.Fatal("an adopted copy is the third-party repo's own, with its own git, not on GitHub: walk it alone, as far as deploy")
	case upTo() == connectGithub:
		r.T.Fatal("a stamped copy has its own git, not on GitHub, where connect-github's steps push: walk it as far as deploy")
	}
	return repo{Repo: r}.stampedCopy(version).Repo
}

// prepare is the repo the walk walks instead, when it's not the one the walk made: a third-party Worker adopted as its
// owner would (-adopt), or, for connect-github's steps, the same repo in a checkout of this repo at HEAD, which is what
// Cloudflare builds from GitHub.
func prepare(r walk.Repo) walk.Repo {
	switch adopt := *adoptFlag != ""; {
	case adopt && (*walk.Version != "" || upTo() == connectGithub):
		r.T.Fatal("an adopted copy is the third-party repo's own, with its own git, not on GitHub: walk it alone, as far as deploy")
	case adopt:
		return repo{Repo: walk.At(r.T, *adoptFlag)}.adoptedCopy().Repo
	case upTo() == connectGithub:
		return repo{Repo: r}.checkoutOfHead().Repo
	}
	return r
}

// walker is the steps run on a repo in their order, as far as upTo, with what they leave for the steps after them.
type walker struct {
	repo
	upTo       stage
	before     walk.Files // the repo's files before the steps that deploy, which must change none of them
	url        string     // the Worker's, once 08-deploy has deployed it
	preview    string     // its preview's, once 11-preview has made it
	prod, prev string     // the throwaway branches connect-github's steps push
}

// walkOf is the walk of a repo the walk has set up: its Worker read as wrangler reads it, and, for a walk that deploys,
// whatever a stopped one left on Cloudflare deleted first, and deleted again when this one fails.
func walkOf(r walk.Repo) *walker {
	w := &walker{repo: repo{Repo: r, adopted: *adoptFlag != ""}, upTo: upTo()}
	w.readWorker()
	if w.upTo >= deploy {
		w.miseDeleteEverythingOnCloudflare()
		w.T.Cleanup(func() {
			if w.T.Failed() {
				w.Heading("Cleaning up after the failure")
				w.miseDeleteEverythingOnCloudflare()
			}
		})
		w.before = w.Snapshot(local...)
	}
	if w.upTo >= connectGithub {
		// The throwaway branches say whose they are and when (MMDD-HHMM).
		when := time.Now().Format("0102-1504")
		w.prod = "e2e-" + filepath.Base(w.Name) + "-prod-" + when    // e2e-templates-prod-1030-1525
		w.prev = "e2e-" + filepath.Base(w.Name) + "-preview-" + when // e2e-templates-preview-1030-1525
		// Cloudflare names the preview <branch>-<worker>, and that part of its address can't pass 63 characters.
		if label := previewAlias(w.prev) + "-" + w.worker; len(label) > 63 {
			w.T.Fatalf("the preview's address would start %s, %d characters: Cloudflare allows 63", label, len(label))
		}
	}
	return w
}
