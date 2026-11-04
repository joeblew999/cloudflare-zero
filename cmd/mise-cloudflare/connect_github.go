package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/joeblew999/mise-cloudflare/internal/cf"
	"github.com/joeblew999/mise-cloudflare/internal/rules"
	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
	"github.com/joeblew999/mise-cloudflare/internal/wrangler"
)

// githubReport is connect-github -json: how Workers Builds builds each of the repo's Workers now.
type githubReport struct {
	Repository string         `json:"repository"`
	Root       string         `json:"root"`
	Build      string         `json:"build"`
	Token      string         `json:"token"`
	Workers    []githubWorker `json:"workers"`
	Removed    bool           `json:"removed,omitempty"`
	// The previews' own KV, D1 and R2, in the Worker's Previews Base.
	Previews []resource `json:"previews,omitempty"`
	// GitHub deletes merged pull requests' branches, and Workers Builds their previews with them.
	DeleteMergedBranches bool `json:"deleteMergedBranches"`
}

// githubWorker is one Worker Workers Builds deploys: production, or a wrangler environment's (staging).
type githubWorker struct {
	Env       string `json:"env,omitempty"` // "" for production
	Worker    string `json:"worker"`
	ScriptTag string `json:"scriptTag"`
	Branch    string `json:"branch"`
	Deploy    string `json:"deploy"`
	Preview   string `json:"preview,omitempty"` // every other branch's preview; production only
}

// builds is how Workers Builds builds the repo: github's flags, else what Cloudflare has now.
type builds struct{ Branch, Staging, Build string }

// connectGithub sets up Cloudflare's Workers Builds: pushes to the branch deploy production, other branches get a preview,
// and --staging adds a staging branch. It uses cf's login, since wrangler's can't hold Workers Builds' scope. --off
// removes it all.
func (r repo) connectGithub(ctx context.Context, b builds, off, jsonOut bool) {
	if !cf.Installed() {
		fail(errors.New("cf, which connects Workers Builds, doesn't run here: run mise run cloudflare:09-connect-github, which brings it"))
	}
	r.refuseStrayTokens()
	// The GitHub repository first: without one there's nothing for Workers Builds to build, so no login is asked for.
	var gh ghRepo
	if !off {
		var err error
		if gh, err = githubRepo(ctx, r.dir); err != nil {
			fail(err)
		}
	}
	must(rules.EnsureIgnored(r.dir, ".cloudflare/")) // cf caches the account it logs into there
	c := cf.CF{Dir: r.dir, Profile: r.w.Profile}
	who, err := c.Whoami(ctx)
	if err != nil {
		fail(err)
	}
	if !who.Authenticated || !who.HasScopes() {
		must(c.Login(ctx, !wrangler.Device()))
		if who, err = c.Whoami(ctx); err != nil {
			fail(err)
		}
		if !who.Authenticated || !who.HasScopes() {
			fail(fmt.Errorf("the cf login %s doesn't hold %s", c.Profile, strings.Join(cf.Scopes, " ")))
		}
	}
	if len(who.Accounts) != 1 {
		fail(fmt.Errorf("the cf login %s can use %d accounts; log it in again and pick one", c.Profile, len(who.Accounts)))
	}
	c.Account = who.Accounts[0].ID
	if b == (builds{}) && !off {
		b = r.currentBuilds(ctx, c)
	}

	// The Workers: production, the Worker CLOUDFLARE_ENV picks (none, usually), and staging's if there's a staging branch.
	env := os.Getenv("CLOUDFLARE_ENV")
	envFlag := func(e string) string {
		if e == "" {
			return ""
		}
		return " --env " + e
	}
	workers := []githubWorker{{Env: env, Worker: r.wc.Name}}
	if b.Staging != "" {
		if !r.wc.HasEnv("staging") {
			fail(fmt.Errorf("a staging branch needs the wrangler environment staging in %s (env.staging, with its own bindings)", filepath.Base(r.wc.Path)))
		}
		sc, err := wconfig.ReadEnv(ctx, r.dir, "staging")
		if err != nil {
			fail(err)
		}
		workers = append(workers, githubWorker{Env: "staging", Worker: sc.Name, Branch: b.Staging, Deploy: "npx wrangler deploy --env staging"})
	}
	for i := range workers {
		if off {
			workers[i].ScriptTag = findWorker(ctx, c, workers[i].Worker) // "" if it isn't deployed: then nothing builds it
		} else {
			workers[i].ScriptTag = workerTag(ctx, c, workers[i].Worker, workers[i].Env)
		}
	}
	rep := githubReport{Workers: workers}

	if off {
		for _, w := range workers {
			if w.ScriptTag == "" {
				continue
			}
			if err := c.JSON(ctx, nil, "builds", "workers", "delete", w.ScriptTag, "--force"); err != nil && !cf.NotFound(err) {
				fail(err)
			}
		}
		rep.Removed = true
		rep.Previews = r.previewResources(ctx, true, jsonOut)
		if jsonOut {
			emit(rep)
		} else {
			fmt.Println("✓ Workers Builds no longer builds " + workerNames(workers))
		}
		return
	}

	// The build token Workers Builds deploys with: the account's first, which Cloudflare made (connecting a repository
	// in the dashboard makes one). It covers Workers Scripts, KV, R2 and Workers Routes; a Worker binding anything else
	// (D1, Queues, …) needs that permission added to it in the dashboard.
	tokens, err := buildTokens(ctx, c)
	if err != nil {
		fail(err)
	}
	var tokenName string
	if len(tokens) > 0 {
		rep.Token, tokenName = tokens[0].UUID, tokens[0].Name
	}
	if rep.Token == "" {
		fail(errors.New("the account has no Workers Builds token yet: connect any repository once in Cloudflare's dashboard (a Worker's Settings, Builds), which makes one, then run this again"))
	}
	rep.Repository = gh.Owner.Login + "/" + gh.Name
	rep.Root = gh.root
	_, install := rules.Lockfile(r.dir)
	if run := rules.Build(r.dir); run != "" {
		install += " && " + run // the package's own build, before wrangler deploys what it writes
	}
	rep.Build = orDefault(b.Build, install)
	prodBranch := gh.DefaultBranch
	if b.Staging != "" {
		prodBranch = "production"
	}
	workers[0].Branch = orDefault(b.Branch, prodBranch)
	workers[0].Deploy = "npx wrangler deploy" + envFlag(env)
	workers[0].Preview = "npx wrangler preview" + envFlag(env)
	// Worker Previews (wrangler preview) need a previews block, and one whose KV, D1 or R2 names no resource Cloudflare
	// refuses; wrangler preview itself names the bindings a preview lacks.
	if problems := rules.Previews(r.wc, true); len(problems) > 0 {
		fail(errors.New(strings.Join(problems, "\n")))
	}
	if len(workers) > 1 && workers[0].Branch == workers[1].Branch {
		fail(fmt.Errorf("production and staging both deploy from %s: give them different branches", workers[0].Branch))
	}
	for _, w := range workers {
		previews := w.Preview != ""
		// Which repository: create takes it, update can't change it (cf builds workers update has only the branch).
		repository := []string{
			"--git-repository-provider-type", "github",
			"--git-repository-provider-account-id", fmt.Sprint(gh.Owner.ID),
			"--git-repository-provider-account-name", gh.Owner.Login,
			"--git-repository-repo-id", fmt.Sprint(gh.ID),
			"--git-repository-repo-name", gh.Name,
		}
		settings := []string{
			"--git-repository-branch", w.Branch,
			"--production-settings-build-command", rep.Build,
			"--production-settings-deploy-command", w.Deploy,
			"--production-settings-build-token-uuid", rep.Token,
			"--production-settings-root-directory", rep.Root,
			fmt.Sprintf("--previews-enabled=%v", previews),
			"--previews-base-config-build-command", rep.Build,
			"--previews-base-config-deploy-command", orDefault(w.Preview, "npx wrangler preview"+envFlag(w.Env)),
			"--previews-base-config-build-token-uuid", rep.Token,
			"--previews-base-config-root-directory", rep.Root,
		}
		var now struct {
			Git struct {
				RepoID json.RawMessage `json:"repo_id"`
			} `json:"git_repository"`
		}
		connected := c.JSON(ctx, &now, "builds", "workers", "get", w.ScriptTag) == nil
		if id := strings.Trim(string(now.Git.RepoID), `"`); connected && id != "" && id != fmt.Sprint(gh.ID) {
			// It builds from another repository (this one moved, say), which update can't change: made afresh.
			if err := c.JSON(ctx, nil, "builds", "workers", "delete", w.ScriptTag, "--force"); err != nil && !cf.NotFound(err) {
				fail(fmt.Errorf("%s builds from another repository, and its connection couldn't be removed to connect %s: %w", w.Worker, rep.Repository, err))
			}
			connected = false
		}
		if connected {
			if err := c.JSON(ctx, nil, append([]string{"builds", "workers", "update", w.ScriptTag}, settings...)...); err != nil {
				fail(fmt.Errorf("%s: %w", w.Worker, err))
			}
		} else if err := c.JSON(ctx, nil, append(append([]string{"builds", "workers", "create", "--script-tag", w.ScriptTag}, repository...), settings...)...); err != nil {
			fail(fmt.Errorf("%s: %w\nIf Cloudflare can't see the repository, give Cloudflare's GitHub app access to %s on GitHub (Settings, Applications, Cloudflare Workers and Pages), then run this again", w.Worker, err, rep.Repository))
		}
	}
	// Previews get their own KV, D1 and R2, which wrangler preview doesn't make.
	rep.Previews = r.previewResources(ctx, false, jsonOut)
	// A branch's preview goes when the branch does; GitHub deletes a pull request's branch when it merges, with this on.
	rep.DeleteMergedBranches = deleteMergedBranches(ctx, gh)
	if jsonOut {
		emit(rep)
		return
	}
	fmt.Printf("✓ Workers Builds builds from %s, in %s, with %s:\n", rep.Repository, rep.Root, rep.Build)
	for _, w := range workers {
		name := "production"
		if w.Env != "" {
			name = w.Env
		}
		fmt.Printf("  %-10s %s: a push to %s runs %s\n", name, w.Worker, w.Branch, w.Deploy)
	}
	fmt.Printf("  previews   any other branch: %s, its URL on the pull request\n", workers[0].Preview)
	fmt.Printf("  with the account's build token %q; GitHub holds no Cloudflare credential\n", tokenName)
	fmt.Printf("  · a build that fails for a permission says which in its log: give the build token %q it (Cloudflare's dashboard: Manage account, Account API tokens, %s, Edit)\n", tokenName, tokenName)
	if rep.DeleteMergedBranches {
		fmt.Printf("  GitHub deletes a pull request's branch when it merges, and Workers Builds its preview with it\n")
	} else {
		fmt.Printf("  · log gh in (gh auth login), or switch on GitHub's \"Automatically delete head branches\" (%s's Settings, General), so merged pull requests' previews go too\n", rep.Repository)
	}
}

func workerNames(ws []githubWorker) string {
	var names []string
	for _, w := range ws {
		names = append(names, w.Worker)
	}
	return strings.Join(names, " and ")
}

// workerTag is a deployed Worker's tag, which Workers Builds attaches to, as Cloudflare has it (cf workers scripts
// search: its id). The search matches names partly, so only the exact name counts.
func workerTag(ctx context.Context, c cf.CF, name, env string) string {
	if tag := findWorker(ctx, c, name); tag != "" {
		return tag
	}
	deploy := "mise run cloudflare:08-deploy"
	if env != "" {
		deploy += " -- --env " + env
	}
	fail(fmt.Errorf("Workers Builds attaches to a deployed Worker, and %s isn't deployed: deploy it once first (%s), then run this again", name, deploy))
	return ""
}

// findWorker is the Worker's tag on Cloudflare, "" if it isn't there.
func findWorker(ctx context.Context, c cf.CF, name string) string {
	var found []struct {
		ID   string `json:"id"`
		Name string `json:"script_name"`
	}
	if c.JSON(ctx, &found, "workers", "scripts", "search", "--name", name) != nil {
		return ""
	}
	for _, f := range found {
		if f.Name == name {
			return f.ID
		}
	}
	return ""
}

// currentBuilds is how Workers Builds builds the repo's Workers now, empty if it doesn't.
func (r repo) currentBuilds(ctx context.Context, c cf.CF) builds {
	var b builds
	get := func(worker string) (branch, build string) {
		var w struct {
			Git struct {
				Branch string `json:"branch"`
			} `json:"git_repository"`
			Production struct {
				Build string `json:"build_command"`
			} `json:"production_settings"`
		}
		if tag := findWorker(ctx, c, worker); tag != "" && c.JSON(ctx, &w, "builds", "workers", "get", tag) == nil {
			return w.Git.Branch, w.Production.Build
		}
		return "", ""
	}
	b.Branch, b.Build = get(r.wc.Name)
	if r.wc.HasEnv("staging") {
		if sc, err := wconfig.ReadEnv(ctx, r.dir, "staging"); err == nil {
			b.Staging, _ = get(sc.Name)
		}
	}
	return b
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// ghRepo is the repo's GitHub repository, as GitHub's API has it, and the repo folder's path in it.
type ghRepo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	Owner         struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"owner"`
	root string
}

var githubURL = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(\.git)?$`)

// githubRepo is the GitHub repository of the git repo dir is in (its origin remote), from GitHub's API, with
// GITHUB_TOKEN when there is one (a private repository needs it).
func githubRepo(ctx context.Context, dir string) (ghRepo, error) {
	var g ghRepo
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return g, errors.New("this repo has no origin remote: Workers Builds builds from a GitHub repository")
	}
	m := githubURL.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return g, fmt.Errorf("origin isn't a GitHub repository (%s): Workers Builds here builds from GitHub", strings.TrimSpace(string(out)))
	}
	top, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return g, err
	}
	realTop, _ := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	realDir, _ := filepath.EvalSymlinks(dir)
	rel, err := filepath.Rel(realTop, realDir)
	if err != nil {
		return g, err
	}
	g.root = "/" // as Cloudflare writes it, the repository's root
	if rel != "." {
		g.root = "/" + filepath.ToSlash(rel)
	}
	for _, token := range []string{githubToken(ctx), ""} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+m[1]+"/"+m[2], nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return g, err
		}
		err = json.NewDecoder(resp.Body).Decode(&g)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && err == nil && g.ID != 0 {
			return g, nil
		}
	}
	return g, fmt.Errorf("GitHub doesn't show the repository %s/%s: a private one needs a GitHub login that can read it: gh auth login (mise exec gh -- gh auth login where gh isn't installed), then run this again (or set GITHUB_TOKEN to such a token)", m[1], m[2])
}

// githubToken is the GitHub login to use: GITHUB_TOKEN or GH_TOKEN, else gh's own (gh auth token), else none.
func githubToken(ctx context.Context) string {
	if token := cmpOrEnv("GITHUB_TOKEN", "GH_TOKEN"); token != "" {
		return token
	}
	if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func cmpOrEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

type buildToken struct {
	UUID    string `json:"build_token_uuid"`
	Name    string `json:"build_token_name"`
	TokenID string `json:"cloudflare_token_id"`
}

func buildTokens(ctx context.Context, c cf.CF) ([]buildToken, error) {
	var tokens []buildToken
	err := c.JSON(ctx, &tokens, "builds", "tokens", "list")
	return tokens, err
}

// deleteMergedBranches turns on GitHub's "Automatically delete head branches", so a merged preview goes with its
// branch. It reports whether it's on; without a GitHub login it leaves it to the person.
func deleteMergedBranches(ctx context.Context, g ghRepo) bool {
	token := githubToken(ctx)
	if token == "" {
		return false
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, "https://api.github.com/repos/"+g.Owner.Login+"/"+g.Name,
		strings.NewReader(`{"delete_branch_on_merge":true}`))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var r struct {
		On bool `json:"delete_branch_on_merge"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&r) == nil && r.On
}
