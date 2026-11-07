// The repo, and the calls into its own mise file, made in the repo as a person there makes them. Never the
// root's mise.toml.

package e2e

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/joeblew999/mise-check/walk"
	"github.com/joeblew999/mise-cloudflare/internal/rules"
	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
)

// repo is a Worker repo that uses mise-cloudflare, walked through its own mise file: walk.Repo (this repo, Top, whose
// GitHub the throwaway branches go to; the repo's name, and its folder), and what the walk reads of its Worker.
type repo struct {
	walk.Repo
	worker  string
	login   string   // the repo's login: named after its top level's Worker, whichever environment the walk deploys
	secrets []string // the Worker's required secrets
	adopted bool     // a third-party Worker adopted (adoptedCopy), not the template's
}

// checkoutOfHead is the same repo in a checkout of this repo at HEAD, which is what Cloudflare builds from GitHub:
// connect-github's steps push it. The checkout goes when the test ends.
func (r repo) checkoutOfHead() repo {
	r.T.Helper()
	tmp, err := filepath.EvalSymlinks(walk.LongPath(r.T.TempDir())) // the real path: macOS's /var is /private/var, and mise stops only there
	if err != nil {
		r.T.Fatal(err)
	}
	checkout := filepath.Join(tmp, "checkout")
	if out, err := exec.Command("git", "-C", r.Top, "worktree", "add", "-q", "--detach", checkout, "HEAD").CombinedOutput(); err != nil {
		r.T.Fatalf("git worktree add: %v\n%s", err, out)
	}
	top := r.Top
	r.T.Cleanup(func() { exec.Command("git", "-C", top, "worktree", "remove", "--force", checkout).Run() })
	from := r.Dir
	r.Top, r.Dir = checkout, filepath.Join(checkout, r.Name)
	// The machine's own override isn't committed (mise run tool:install writes it): without it the checkout would run
	// the release the repo pins, not this checkout's build.
	if b, err := os.ReadFile(filepath.Join(from, ".mise", "config.local.toml")); err == nil {
		if err := os.WriteFile(filepath.Join(r.Dir, ".mise", "config.local.toml"), b, 0o644); err != nil {
			r.T.Fatal(err)
		}
	}
	return r
}

// The stamped copy's Worker is its own, so its walk never touches the template's: its folder's name, as template
// names a Worker.
const stampedWorker = "mise-cloudflare-new"

// stampedCopy is a new repo, made as a person makes one: an empty folder with its own git, then mise-cloudflare init,
// run at version, a release from GitHub, or this checkout's build (local) when it is "" (init pins mise-cloudflare at
// itself, in .mise/config.toml), then the repo's own cloudflare:template, which writes the template's Worker named for
// the folder. It is in a folder of its own that goes when the test ends; its logins are wrangler's and cf's on this
// machine, so they stay.
func (r repo) stampedCopy(version string) repo {
	r.T.Helper()
	n := repo{Repo: r.New(stampedWorker)}
	at := cmp.Or(strings.TrimPrefix(version, "v"), "local")
	n.miseInit(at)
	if b, _ := os.ReadFile(filepath.Join(n.Dir, ".mise", "tasks", "mise-cloudflare.toml")); !bytes.Contains(b, []byte(`["cloudflare:template"]`)) {
		r.T.Fatalf("mise-cloudflare %s has no cloudflare:template: a release from before it can't make a new repo", at)
	}
	n.Name = "a new repo"
	n.MustTask("cloudflare:template")
	if b, _ := os.ReadFile(filepath.Join(n.Dir, "wrangler.jsonc")); !bytes.Contains(b, []byte(`"`+stampedWorker+`"`)) {
		r.T.Fatalf("cloudflare:template didn't name the Worker for its folder, %s: the walk would walk %s's", stampedWorker, r.Name)
	}
	n.Pass("%s, %s: an empty folder, its own git, mise-cloudflare init at %s, then cloudflare:template (the template's Worker, named for the folder)", n.Name, stampedWorker, at)
	if withExtras() {
		n.addExtras()
	}
	if env := withEnv(); env != "" {
		n.addEnv(env)
	}
	return n
}

// readWorker reads the repo's Worker as wrangler does, once walk has set the repo up (mise install, then its setup task:
// an adopted copy has none, its packages are its own install's): its name, its login and its required secrets.
func (r *repo) readWorker() {
	r.T.Helper()
	c, err := wconfig.Read(context.Background(), r.Dir)
	if err != nil {
		r.T.Fatal(err)
	}
	r.worker, r.login, r.secrets = c.Name, cmp.Or(c.Login, c.Name), c.Secrets
	r.Pass("%s's Worker is %s", r.Name, r.worker)
	r.Pass("%s runs %s", r.Name, r.versions())
}

// versions is what the repo runs, each as its own tool reports it: its mise tools (mise ls --local --json), the build
// of mise-cloudflare (cloudflare:show-version) and the wrangler mise run setup installed (its package.json).
func (r repo) versions() string {
	r.T.Helper()
	var tools map[string][]struct {
		Version string
		Active  bool
	}
	if err := json.Unmarshal(r.Stdout("mise", "ls", "--local", "--json"), &tools); err != nil || len(tools) == 0 {
		r.T.Fatalf("the repo's mise tools, mise ls --local --json: %v (%d read)", err, len(tools))
	}
	var v []string
	for _, name := range slices.Sorted(maps.Keys(tools)) {
		for _, t := range tools[name] {
			if t.Active {
				v = append(v, name+" "+t.Version)
			}
		}
	}
	build := strings.TrimSpace(string(r.Stdout("mise", "run", "cloudflare:show-version")))
	var w struct{ Version string }
	b, err := os.ReadFile(filepath.Join(r.Dir, "node_modules", "wrangler", "package.json"))
	if err == nil {
		err = json.Unmarshal(b, &w)
	}
	if err != nil || w.Version == "" || build == "" {
		r.T.Fatalf("the installed wrangler (%q, %v) or mise-cloudflare's build (%q)", w.Version, err, build)
	}
	return fmt.Sprintf("%s (mise.toml); mise-cloudflare's build %s; wrangler %s, installed", strings.Join(v, ", "), build, w.Version)
}

// miseInit runs mise-cloudflare init at the release at (local: this checkout's build) in the repo, on its own mise
// only, as a person runs it, and checks it pinned itself.
func (r repo) miseInit(at string) {
	r.T.Helper()
	r.Must("mise", "exec", "github:joeblew999/mise-cloudflare@"+at, "--", "mise-cloudflare", "init")
	want := `"github:joeblew999/mise-cloudflare" = "` + at + `"`
	if b, _ := os.ReadFile(filepath.Join(r.Dir, ".mise", "config.toml")); !bytes.Contains(b, []byte(want)) {
		r.T.Fatalf("mise-cloudflare %s's init didn't pin itself in .mise/config.toml (%s): a release from before it did can't make a new repo", at, want)
	}
}

// adoptedCopy is a third-party Worker of the corpus (its folder in a repos:check clone) adopted as its owner would: a
// copy in a repo of its own (its own git, in a folder that goes when the walk ends), mise-cloudflare init at this
// checkout's build, its dependencies installed its own way (its lockfile's command), and its wrangler moved as the tool
// says when it has no auth profiles (mise use aube, then cloudflare:update).
func (r repo) adoptedCopy() repo {
	r.T.Helper()
	if _, err := os.Stat(filepath.Join(r.Dir, "package.json")); err != nil {
		r.T.Fatalf("%s isn't a Worker's folder (mise run repos:check clones the corpus into repos/): %v", r.Name, err)
	}
	n := repo{Repo: r.New(filepath.Base(r.Dir)), adopted: true}
	if err := walk.CopyRepo(r.Dir, n.Dir, local...); err != nil {
		r.T.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=walk", "-c", "user.email=walk@example.invalid", "commit", "-qm", "the repo as its owner has it"}} {
		if out, err := exec.Command("git", append([]string{"-C", n.Dir}, args...)...).CombinedOutput(); err != nil {
			r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	n.miseInit("local")
	n.Name = filepath.Base(n.Dir) + " (adopted)"
	_, install := rules.Lockfile(n.Dir)
	f := strings.Fields(install)
	n.Must(f[0], f[1:]...)
	n.Pass("%s: %s copied into a repo of its own with its own git, mise-cloudflare init at local, then its own install (%s)", n.Name, r.Name, install)
	if !n.miseShowJSON().Profiles {
		n.Must("mise", "use", "aube")
		n.MustTask("cloudflare:update")
		if !n.miseShowJSON().Profiles {
			r.T.Fatal("cloudflare:update didn't move its wrangler to one with auth profiles")
		}
		n.Pass("its wrangler had no auth profiles, as cloudflare:check says: moved as the tool says (mise use aube, cloudflare:update)")
	}
	return n
}

// local is what isn't a repo's own, wherever it is, which the walk neither copies nor counts as a step's change:
// installed packages, what wrangler leaves (.wrangler), and local secrets (.dev.vars).
var local = []string{"node_modules", ".wrangler", ".dev.vars"}

// mustIn runs a command in another folder (this repo's own checkout, a worktree of it), its output shown.
func (r repo) mustIn(dir, name string, args ...string) {
	r.T.Helper()
	if _, err := walk.Exec(dir, os.Environ(), "", name, args...); err != nil {
		r.T.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
}

// miseWranglerStdout is what a wrangler command prints on stdout, on the repo's login (cloudflare:run-wrangler): the JSON
// or the list itself. What mise prints around it ("[cloudflare:run-wrangler] $ …", which starts like JSON) goes to
// stderr.
func (r repo) miseWranglerStdout(args ...string) string {
	r.T.Helper()
	cmd := exec.Command("mise", append([]string{"run", "cloudflare:run-wrangler"}, args...)...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "MISE_CEILING_PATHS="+filepath.Dir(r.Dir), "MISE_TRUSTED_CONFIG_PATHS="+r.Dir, "MISE_YES=1", "MISE_TASK_OUTPUT=interleave")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		r.T.Fatalf("wrangler %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

// shown is what the repo's cloudflare:show shows, as JSON (--json).
type shown struct {
	Profile  string   `json:"profile"`
	LoggedIn bool     `json:"loggedIn"`
	Profiles bool     `json:"profiles"`
	Needs    []string `json:"needs"`
	Deployed bool     `json:"deployed"`
	URL      string   `json:"url"`
	GitHub   string   `json:"github"`
	Next     string   `json:"next"`
}

// miseShowJSON is the repo's cloudflare:show --json, its output unprefixed (and only its stdout read) so its JSON reads
// as JSON.
func (r repo) miseShowJSON() shown {
	r.T.Helper()
	cmd := exec.Command("mise", "run", "cloudflare:show", "--json")
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "MISE_CEILING_PATHS="+filepath.Dir(r.Dir), "MISE_TRUSTED_CONFIG_PATHS="+r.Dir, "MISE_TASK_OUTPUT=interleave")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	var s shown
	if err == nil {
		err = json.NewDecoder(strings.NewReader(jsonPart(string(out)))).Decode(&s)
	}
	if err != nil {
		r.T.Fatalf("cloudflare:show --json: %v\n%s", err, out)
	}
	return s
}

// miseShowSaysNext fails unless cloudflare:show says to run want next.
func (r repo) miseShowSaysNext(want string) {
	r.T.Helper()
	if s := r.miseShowJSON(); !strings.Contains(s.Next, want) {
		r.T.Fatalf("cloudflare:show says next: %q, not %q", s.Next, want)
	}
	r.Pass("cloudflare:show says next: %s", want)
}

// miseServeLocally runs the Worker as a person does, with the repo's cloudflare:serve (which makes .dev.vars from
// .dev.vars.example): with no Cloudflare login anywhere it could see unless onLogin (a home of its own, with mise's own
// folders kept, its config among them, so this machine's tools are still there: a repo of someone else's often pins
// no node of its own). It returns the Worker's answer, or, when cloudflare:serve stopped without one,
// what it printed.
func (r repo) miseServeLocally(onLogin bool) (answer []byte, stopped string) {
	r.T.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.T.Fatal(err)
	}
	port := fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	var doctor struct {
		Dirs struct{ Data, Cache, State, Config string }
	}
	if out, err := exec.Command("mise", "doctor", "--json").Output(); err != nil || json.Unmarshal(out, &doctor) != nil {
		r.T.Fatalf("mise doctor --json: %v", err)
	}
	cmd := exec.Command("mise", "run", "cloudflare:serve", "--ip", "127.0.0.1", "--port", port)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "MISE_CEILING_PATHS="+filepath.Dir(r.Dir), "MISE_YES=1", "MISE_TRUSTED_CONFIG_PATHS="+r.Dir,
		"CLOUDFLARE_API_TOKEN=", "CI=1")
	if !onLogin {
		home := r.T.TempDir()
		cmd.Env = append(cmd.Env, "HOME="+home, "XDG_CONFIG_HOME="+home,
			"MISE_DATA_DIR="+doctor.Dirs.Data, "MISE_CACHE_DIR="+doctor.Dirs.Cache, "MISE_STATE_DIR="+doctor.Dirs.State, "MISE_CONFIG_DIR="+doctor.Dirs.Config)
	}
	walk.OwnGroup(cmd)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		r.T.Fatal(err)
	}
	defer walk.KillGroup(cmd)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for i := 0; i < 90; i++ {
		select {
		case <-done:
			return nil, logs.String()
		default:
		}
		if resp, err := http.Get("http://127.0.0.1:" + port + "/"); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return body, ""
		}
		time.Sleep(time.Second)
	}
	r.T.Fatalf("cloudflare:serve never answered:\n%s", logs.String())
	return nil, ""
}

// miseDeleteEverythingOnCloudflare deletes everything of the repo's Worker on Cloudflare, with the repo's own tasks, so a
// walk that deploys starts from nothing whatever a stopped one left: Workers Builds' setup for it
// (cloudflare:connect-github --off, when cf has a login for it; without one there's none), connect-github's
// throwaway e2e-* branches on this repo's GitHub, and the Worker with its D1, KV and R2 and its previews'
// (cloudflare:delete). Then nothing of it may be on the account.
func (r repo) miseDeleteEverythingOnCloudflare() {
	r.T.Helper()
	r.Heading("Nothing of %s on Cloudflare: what a stopped walk left, deleted", r.worker)
	if out, _ := r.Run(nil, "cf", "auth", "whoami", "--profile", r.login); strings.Contains(out, `"authenticated": true`) {
		r.MustTask("cloudflare:connect-github", "--off")
	}
	heads, err := walk.Exec(r.Top, os.Environ(), "", "git", "ls-remote", "--heads", "origin")
	if err != nil {
		r.T.Fatalf("git ls-remote origin: %v", err)
	}
	for _, line := range strings.Split(heads, "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.HasPrefix(f[1], "refs/heads/e2e-") {
			b := strings.TrimPrefix(f[1], "refs/heads/")
			if _, err := walk.Exec(r.Top, os.Environ(), "", "git", "push", "-q", "origin", "--delete", b); err != nil {
				r.T.Fatalf("deleting the branch %s: %v", b, err)
			}
			r.Pass("deleted the branch %s", b)
		}
	}
	r.MustTask("cloudflare:delete", "--yes")
	r.nothingOnCloudflare()
}
