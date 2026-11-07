// Command mise-cloudflare gives a repo its own Cloudflare login and runs the repo's own wrangler on it: no API tokens
// to make, nothing machine-wide, the same on a Mac and in a Claude Code cloud session. The login is a wrangler auth
// profile named after the Worker. `mise-cloudflare show-help` lists the commands.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/joeblew999/mise-check/toolfiles"

	"github.com/joeblew999/mise-cloudflare/internal/rules"
	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
	"github.com/joeblew999/mise-cloudflare/internal/wrangler"
)

const help = `This repo's Cloudflare Worker, with mise-cloudflare. Each command is a task: mise run cloudflare:<command>
runs mise-cloudflare <command>.

The steps, in the order a repo takes them (mise run cloudflare says which is next):
  mise run cloudflare:init                  mise-cloudflare's files written into the repo (again, after upgrading)
  mise run cloudflare:check                 the repo's rules: no tokens or account IDs in it, secrets git-ignored, files current
  mise run cloudflare:show                  what's done, and what to run next (mise run cloudflare, too)
  mise run cloudflare:serve                 the Worker locally, with local D1, KV and R2: no account needed
  mise run cloudflare:set-secrets           the secrets the Worker requires that Cloudflare doesn't have yet
  mise run cloudflare:deploy                the Worker on Cloudflare, on the repo's login: the first time, you click Allow
  mise run cloudflare:connect-github        Cloudflare deploys every push: a preview for every pull request, then
                                            production (--staging main for staging; --off to stop)
  mise run cloudflare:set-preview-secrets   the secrets its previews require, which they don't inherit
  mise run cloudflare:preview               a preview of what's here now, at its own URL (--name my-test)
  mise run cloudflare:login                 the repo's Cloudflare login (the others make it when they need it)
  mise run cloudflare:delete                deploy undone: the Worker and what deploy made, its previews' own (--yes: don't ask)
  mise run cloudflare:logout                the repo's logins logged out and deleted
  mise run cloudflare:clean                 the local state undone: .wrangler/ and .dev.vars (--yes: don't ask)

And:
  mise run cloudflare:verify         what the repo's GitHub workflow runs: its setup and test tasks, then check
  mise run cloudflare:run-wrangler   any other wrangler command, on the repo's login: d1 list
  mise run cloudflare:template       the template's own Worker, in a repo with none yet: it never writes over a file
  mise run cloudflare:show-version   this build's version
  mise run cloudflare:show-help      this

The repo's pins move by mise-check's own tasks, which init gives it: mise run mise-check:update.
`

func main() {
	// The command first, then its flags. serve, deploy, preview and run-wrangler pass everything after them to wrangler.
	args := os.Args[1:]
	command := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	passes := map[string]bool{"serve": true, "deploy": true, "preview": true, "run-wrangler": true}
	flags := flag.NewFlagSet("mise-cloudflare "+command, flag.ExitOnError)
	jsonOut := flags.Bool("json", false, "print one JSON document on stdout instead of text")
	off := flags.Bool("off", false, "connect-github: turn it off")
	branch := flags.String("branch", "", "connect-github: the branch whose pushes deploy production (default the repository's default branch, or production with --staging)")
	staging := flags.String("staging", "", "connect-github: a staging branch, whose pushes deploy the wrangler environment env.staging")
	build := flags.String("build", "", "connect-github: the build command (default installing from the lockfile: npm ci, …)")
	yes := flags.Bool("yes", false, "delete, clean: don't ask first")
	flags.Usage = func() { fmt.Fprint(os.Stderr, help) }
	if !passes[command] {
		flags.Parse(args)
		if flags.NArg() > 0 {
			flags.Usage()
			os.Exit(2)
		}
		args = nil
	}
	ctx := context.Background()
	dir, err := os.Getwd()
	must(err)
	switch command {
	case "show-version":
		showVersion()
		return
	case "show-help":
		showHelp()
		return
	case "init": // mise-cloudflare's files, then mise-check's own, by its init: a repo runs mise run mise-check:<command> itself
		initRepo(dir)
		must(toolfiles.MiseCheckInit(dir))
		toolfiles.Trust(dir, ".mise/config.toml", nil)
		sayNewest()
		return
	case "template": // a repo with no Worker yet; it never writes over a file, so it runs in CI too (the walk there makes a new repo with it)
		if templateRepo(dir) {
			setUpWorker(ctx, dir)
		}
		return
	case "verify": // before the repo is opened: its setup installs the wrangler that needs
		verify(ctx, dir)
		return
	}
	// CI only checks the repo; Cloudflare's Workers Builds deploys. No one is there to click Allow.
	if inCI() && command != "" && command != "show" && command != "check" && command != "verify" && command != "serve" && command != "clean" {
		fail(errors.New("in CI there's no one to log in: CI runs mise run cloudflare:verify, and Cloudflare's Workers Builds deploys (mise run cloudflare:connect-github)"))
	}
	r := openRepo(ctx, dir)
	switch command {
	case "", "show":
		r.show(ctx, *jsonOut)
	case "check":
		r.check(ctx, *jsonOut)
	case "serve":
		r.serve(ctx, args)
	case "set-secrets":
		r.setSecrets(ctx, false)
	case "set-preview-secrets":
		r.setPreviewSecrets(ctx)
	case "deploy":
		r.deploy(ctx, args)
	case "connect-github":
		r.connectGithub(ctx, builds{Branch: *branch, Staging: *staging, Build: *build}, *off, *jsonOut)
	case "preview":
		r.preview(ctx, args)
	case "run-wrangler":
		r.runWrangler(ctx, args)
	case "login":
		r.login(ctx, *jsonOut)
	case "delete":
		r.delete(ctx, *yes)
	case "logout":
		r.logout(ctx)
	case "clean":
		r.clean(*yes)
	default:
		fmt.Fprint(os.Stderr, help)
		os.Exit(2)
	}
}

// repo is a Worker repo with its wrangler installed: its config as wrangler reads it, and wrangler on its login.
type repo struct {
	dir string
	wc  wconfig.Config
	w   wrangler.W
}

func openRepo(ctx context.Context, dir string) repo {
	w := wrangler.W{Dir: dir}
	if !hasWorker(dir) {
		fail(errNoWorker)
	}
	if !w.Installed() {
		install := "mise run setup"
		if has := exec.CommandContext(ctx, "mise", "tasks", "info", "setup"); has.Run() != nil {
			_, install = rules.Lockfile(dir)
		}
		fail(errors.New("wrangler isn't installed here: install the repo's dependencies, " + install))
	}
	// wrangler runs on node. The repo's mise.toml pins it (the template's does), and a mise task puts it on the PATH.
	if exec.Command("node", "--version").Run() != nil {
		fail(errors.New("node, which wrangler runs on, doesn't run here: run this through its mise task (mise run cloudflare…), which brings the node the repo's mise.toml pins; a repo with no pin needs one: mise use node@lts"))
	}
	wc, err := wconfig.Read(ctx, dir)
	must(err)
	if wc.Name == "" {
		fail(fmt.Errorf("%s has no name: add one (\"name\"), which wrangler deploy needs and the repo's login is named after", filepath.Base(wc.Path)))
	}
	// The login is named after the Worker at the config's top level: one login for the repo, whichever environment
	// wrangler deploys (CLOUDFLARE_ENV), as they're all on its account.
	w.Profile = cmp.Or(wc.Login, wc.Name)
	return repo{dir: dir, wc: wc, w: w}
}

// inCI reports whether this runs in CI (CI=true, as GitHub Actions and every other CI set it).
func inCI() bool {
	return os.Getenv("CI") != "" && os.Getenv("CI") != "false" && os.Getenv("CI") != "0"
}

func exitCode(err error) int {
	var e *exec.ExitError
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return 1
}

func emit(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	must(enc.Encode(v))
}

func must(err error) {
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mise-cloudflare:", err)
	os.Exit(1)
}

// version is set by the release build (goreleaser's -X main.version); otherwise Go's module version, or "(devel)".
var version = ""

func buildVersion() string {
	if version != "" {
		return strings.TrimPrefix(version, "v")
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "(devel)"
}
