package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/joeblew999/mise-cloudflare/internal/cf"
	"github.com/joeblew999/mise-cloudflare/internal/rules"
	"github.com/joeblew999/mise-cloudflare/internal/wrangler"
)

// statusReport is mise-cloudflare -json: where the repo is, and what to run next.
type statusReport struct {
	Worker         string             `json:"worker"`
	Profile        string             `json:"profile"`
	LoggedIn       bool               `json:"loggedIn"`
	Profiles       bool               `json:"profiles"` // the repo's wrangler has auth profiles, which the login is
	Accounts       []wrangler.Account `json:"accounts"`
	Scopes         []string           `json:"scopes"` // what the login holds
	Needs          []string           `json:"needs"`  // what it must hold: wrangler's own login's
	Deployed       bool               `json:"deployed"`
	Secrets        []string           `json:"secretsToSet"` // the config's secrets.required not yet on Cloudflare
	URL            string             `json:"url,omitempty"`
	GitHub         string             `json:"github"`                   // "Workers Builds" when it deploys every push, else ""
	GitHubCantTell string             `json:"githubCantTell,omitempty"` // why this machine can't tell whether it does
	// The config's compatibility_date, and the one this wrangler's dev uses when a config gives none.
	CompatibilityDate string   `json:"compatibilityDate"`
	DevDate           string   `json:"devDate"`
	Remote            []string `json:"remote"` // the bindings wrangler runs on Cloudflare even locally: cloudflare:serve needs the login
	Build             string   `json:"build"`  // the package's own build, which runs before wrangler does (rules.Build), or ""
	StrayTokens       []string `json:"strayTokens"`
	Problems          []string `json:"problems"` // cloudflare check's: a deploy would fail on them, or leak
	Next              string   `json:"next"`
}

// show is mise-cloudflare with no command or show (mise run cloudflare, cloudflare:show): the steps in order, which are done for this repo, and the
// next one. It never logs in.
func (r repo) show(ctx context.Context, jsonOut bool) {
	rep := statusReport{Worker: r.wc.Name, Profile: r.w.Profile, CompatibilityDate: r.wc.CompatibilityDate, DevDate: r.wc.DevDate, Remote: append([]string{}, r.wc.Remote...), Build: rules.Build(r.dir), Accounts: []wrangler.Account{}, Scopes: []string{},
		Needs: r.wanted(ctx), StrayTokens: tokenEnvFiles(r.dir), Problems: []string{}}
	if strayInShell() != "" {
		rep.StrayTokens = append([]string{"this shell"}, rep.StrayTokens...)
	}
	// cloudflare check's problems come before deploying: it fails on them, or leaks the login or a secret.
	if problems, err := rules.Check(r.dir, r.wc); err == nil {
		rep.Problems = append(append([]string{}, problems...), r.stale(ctx)...)
	}
	noProfiles := r.noProfiles(ctx)
	rep.Profiles, rep.Problems = len(noProfiles) == 0, append(rep.Problems, noProfiles...)
	rep.LoggedIn = r.w.LoggedIn(ctx)
	if rep.LoggedIn && len(rep.StrayTokens) == 0 {
		must(r.w.Activate(ctx))
		if who, err := r.w.Whoami(ctx); err == nil {
			rep.Accounts, rep.Scopes = who.Accounts, who.Scopes
		}
		rep.Deployed = r.deployed(ctx)
		if rep.Deployed {
			rep.URL = lastDeployURL(r.dir)
		}
	}
	// The secrets the Worker requires that Cloudflare doesn't have yet: a deploy refuses until they're set. wrangler
	// secret put sets one, and makes the Worker if it isn't there yet.
	if len(r.wc.Secrets) > 0 && len(rep.StrayTokens) == 0 {
		var have []struct{ Name string }
		if rep.LoggedIn {
			r.wranglerJSON(ctx, &have, "secret", "list")
		}
		for _, s := range r.wc.Secrets {
			if !slices.ContainsFunc(have, func(h struct{ Name string }) bool { return h.Name == s }) {
				rep.Secrets = append(rep.Secrets, s)
			}
		}
	}
	if rep.Deployed {
		var connected bool
		if connected, rep.GitHubCantTell = r.buildsConnected(ctx); connected {
			rep.GitHub = "Workers Builds"
		}
	}
	// What GitHub deploying needs from the wrangler config first (a previews block with the Worker's bindings), and,
	// once Workers Builds deploys, any KV, D1 or R2 added since the previews' own were made.
	var previewProblems, previewsMissing, previewSecrets []string
	if rep.Deployed && rep.GitHub == "" {
		previewProblems = rules.Previews(r.wc, true)
	}
	if rep.GitHub != "" && len(rep.Accounts) == 1 {
		if token, err := r.w.Token(ctx); err == nil {
			previewsMissing, previewSecrets = r.previewsMissing(ctx, token, rep.Accounts[0].ID)
		}
	}
	switch {
	case len(rep.StrayTokens) > 0:
		rep.Next = "remove the Cloudflare token or key (" + strings.Join(strayVars, ", ") + ") in " + strings.Join(rep.StrayTokens, " and ") + ": it would override the repo's login"
	case len(rep.Problems) > 0:
		rep.Next = "fix what mise run cloudflare:check lists, first: " + rep.Problems[0]
	case len(rep.Secrets) > 0:
		rep.Next = "mise run cloudflare:set-secrets (it asks for " + strings.Join(rep.Secrets, ", ") + ")"
	case !rep.Deployed:
		rep.Next = "mise run cloudflare:deploy"
	case len(previewProblems) > 0:
		rep.Next = previewProblems[0] + "; then mise run cloudflare:connect-github"
	case rep.GitHubCantTell != "":
		rep.Next = "mise run cloudflare: it brings cf, and shows whether Workers Builds deploys it yet"
	case rep.GitHub == "":
		rep.Next = "mise run cloudflare:connect-github"
	case len(previewSecrets) > 0:
		rep.Next = "mise run cloudflare:set-preview-secrets (it asks for " + strings.Join(previewSecrets, ", ") + ": previews don't get the Worker's secrets)"
	case len(previewsMissing) > 0:
		rep.Next = "mise run cloudflare:connect-github: previews don't have their own " + strings.Join(previewsMissing, ", ") + " yet"
	default:
		rep.Next = "push a branch and open a pull request: its preview's link comes on the pull request"
	}
	if jsonOut {
		emit(rep)
		return
	}
	mark := map[bool]string{true: "✓", false: "·"}
	fmt.Printf("%s\n", rep.Worker)
	// The environment is chosen as wrangler chooses it: the top level, unless CLOUDFLARE_ENV names one.
	if env := os.Getenv("CLOUDFLARE_ENV"); env != "" {
		fmt.Printf("  wrangler environment %s (CLOUDFLARE_ENV)\n", env)
	} else if len(r.wc.Envs) > 0 {
		fmt.Printf("  %s's top level; it also has %s: CLOUDFLARE_ENV=%s mise run cloudflare shows that one, and the steps deploy it\n",
			filepath.Base(r.wc.Path), "env."+strings.Join(r.wc.Envs, ", env."), r.wc.Envs[0])
	}
	if rep.Build != "" {
		fmt.Printf("  built first: %s (package.json's build script), before cloudflare:serve, cloudflare:deploy, cloudflare:preview and Cloudflare's builds\n", rep.Build)
	}
	file := filepath.Base(r.wc.Path)
	// wrangler itself says when the date is later than its local runtime has (wrangler dev falls back, and says so).
	if rep.CompatibilityDate == "" {
		fmt.Printf("  ✗ no compatibility_date in %s: wrangler won't deploy without one (wrangler dev uses %s)\n", file, rep.DevDate)
	} else {
		fmt.Printf("  compatibility date %s\n", rep.CompatibilityDate)
	}
	if len(rep.Problems) > 0 {
		fmt.Printf("  ✗ the repo has %d problem(s) to fix first (mise run cloudflare:check lists them)\n", len(rep.Problems))
	}
	run := ""
	if len(rep.Remote) > 0 {
		run = "   " + strings.Join(rep.Remote, ", ") + " on Cloudflare even here, on the repo's login" + map[bool]string{true: "", false: ", which it makes first (you click Allow)"}[rep.LoggedIn]
	}
	fmt.Printf("  ✓ Run it locally     mise run cloudflare:serve%s\n", run)
	fmt.Printf("  %s Deploy it          mise run cloudflare:deploy   %s\n", mark[rep.Deployed], cmp.Or(rep.URL, map[bool]string{true: "deployed", false: "not deployed yet"}[rep.Deployed]))
	if len(rep.Secrets) > 0 {
		fmt.Printf("    first, its secrets: %s (mise run cloudflare:set-secrets)\n", strings.Join(rep.Secrets, ", "))
	}
	switch {
	case rep.GitHub != "":
		fmt.Printf("  ✓ Deploy every push  %s deploys it: a preview for every pull request\n", rep.GitHub)
	case rep.GitHubCantTell != "":
		fmt.Printf("  ? Deploy every push  mise run cloudflare:connect-github   can't tell here whether it's done: %s\n", rep.GitHubCantTell)
	default:
		fmt.Printf("  · Deploy every push  mise run cloudflare:connect-github   a preview for every pull request, then staging and production\n")
	}
	switch {
	case !rep.Profiles:
		fmt.Printf("  login: none; the repo's wrangler, %s, can't hold one (mise run cloudflare:check says why)\n", r.w.Version())
	case !rep.LoggedIn:
		fmt.Printf("  login: none yet; deploying makes it (you click Allow)\n")
	case len(rep.Accounts) > 0:
		more := ""
		if len(wrangler.Missing(rep.Needs, rep.Scopes)) > 0 {
			more = "; it needs more permissions, so the next deploy asks you to Allow"
		}
		fmt.Printf("  login: %s, on %s%s\n", rep.Profile, accountNames(wrangler.Who{Accounts: rep.Accounts}), more)
	}
	fmt.Printf("next: %s\n", rep.Next)
}

// lastDeployURL is the URL wrangler's last deploy reports in its output file, .wrangler/output.json (cloudflare deploy
// has it write there): one JSON entry a line, the deploy's "targets" (its workers.dev one, else the first), or "".
func lastDeployURL(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, ".wrangler", "output.json"))
	url := ""
	for _, line := range bytes.Split(b, []byte("\n")) {
		var e struct {
			Type    string   `json:"type"`
			Targets []string `json:"targets"`
		}
		if json.Unmarshal(line, &e) != nil || e.Type != "deploy" {
			continue
		}
		url = ""
		for _, t := range e.Targets {
			if strings.HasPrefix(t, "https://") && (url == "" || strings.HasSuffix(t, ".workers.dev")) {
				url = t
			}
		}
	}
	return url
}

// deployed reports whether the Worker's code is on Cloudflare: its current version has a compatibility date. That's
// wrangler's own line between the two: it won't upload a Worker without one ("A compatibility_date is required when
// uploading a Worker"), and the placeholder wrangler secret put makes for a Worker that isn't there yet has none
// (createDraftWorker, still so in wrangler 4.155.0).
func (r repo) deployed(ctx context.Context) bool {
	var d struct {
		Versions []struct {
			ID string `json:"version_id"`
		} `json:"versions"`
	}
	if r.wranglerJSON(ctx, &d, "deployments", "status", "--json") != nil || len(d.Versions) == 0 {
		return false
	}
	var v struct {
		Resources struct {
			Runtime struct {
				Date string `json:"compatibility_date"`
			} `json:"script_runtime"`
		} `json:"resources"`
	}
	return r.wranglerJSON(ctx, &v, "versions", "view", d.Versions[0].ID, "--json") == nil && v.Resources.Runtime.Date != ""
}

// buildsConnected reports whether Workers Builds builds the Worker, if this machine has cf logged in for the repo, and,
// when cf doesn't run here, that it can't tell and why.
func (r repo) buildsConnected(ctx context.Context) (connected bool, cantTell string) {
	if !cf.Installed() {
		return false, "cf, which reads Workers Builds, doesn't run here (mise run cloudflare brings it)"
	}
	c := cf.CF{Dir: r.dir, Profile: r.w.Profile}
	who, err := c.Whoami(ctx)
	if err != nil {
		return false, err.Error()
	}
	// Not logged in yet, cf's login is made by cloudflare:connect-github, which then connects it or says it is.
	if !who.Authenticated || len(who.Accounts) != 1 {
		return false, ""
	}
	c.Account = who.Accounts[0].ID
	tag := findWorker(ctx, c, r.wc.Name)
	return tag != "" && c.JSON(ctx, nil, "builds", "workers", "get", tag) == nil, ""
}
