// The steps: the repo's step tasks, cloudflare:init to cloudflare:clean, in their order, each with its check (TestWalk walks them).

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joeblew999/mise-check/walk"
)

// steps are the repo's step tasks (its .mise/tasks/mise-cloudflare.toml, which is the template's,
// templates/.mise/tasks/mise-cloudflare.toml), in the order a person takes them (the docs page's, and what mise run
// cloudflare says next), each with the first stage that runs it and its check: how the walk runs it and checks it did
// what it says, as a person would see it. TestSteps fails unless they and notWalked are exactly the template's tasks.
var steps = []walk.Step[*walker]{
	{Task: "cloudflare:init", Stage: int(serve), Check: func(w *walker) {
		before := w.Snapshot(local...)
		w.MustTask("cloudflare:init")
		if changed := walk.Diff(before, w.Snapshot(local...)); len(changed) > 0 {
			w.T.Fatalf("cloudflare:init changed %s: the repo isn't what init writes", strings.Join(changed, ", "))
		}
		w.Pass("cloudflare:init changed nothing: the repo is what init writes")
	}},
	{Task: "cloudflare:check", Stage: int(serve), Check: func(w *walker) {
		if out := w.MustTask("cloudflare:check"); !strings.Contains(out, "rules: pass") {
			w.T.Fatalf("cloudflare:check: %s", out)
		}
		w.Pass("cloudflare:check: the rules pass")
		// As on GitHub, from a fresh checkout: nothing installed, so verify's own setup has to install wrangler.
		if err := os.RemoveAll(filepath.Join(w.Dir, "node_modules")); err != nil {
			w.T.Fatal(err)
		}
		w.MustTask("cloudflare:verify")
		w.Pass("cloudflare:verify: what the repo's GitHub workflow runs passes, from nothing installed, as a fresh checkout is (its setup and test tasks, then cloudflare:check's)")
	}},
	{Task: "cloudflare:show", Stage: int(serve), Check: func(w *walker) {
		if len(w.secrets) == 0 {
			w.miseShowSaysNext("mise run cloudflare:deploy")
		} else {
			w.miseShowSaysNext("mise run cloudflare:set-secrets")
		}
	}},
	{Task: "cloudflare:serve", Stage: int(serve), Check: func(w *walker) {
		body, stopped := w.miseServeLocally(false)
		if remoteExtras() {
			if body != nil || !strings.Contains(stopped, "AI: wrangler runs it on Cloudflare even locally, which needs the repo's login") {
				w.T.Fatalf("cloudflare:serve with no Cloudflare login and an extra wrangler runs on Cloudflare even locally: %s%s", body, stopped)
			}
			w.Pass("cloudflare:serve with no Cloudflare login, in CI, stops before wrangler dev at the extra wrangler runs on Cloudflare even locally, and says it needs the repo's login")
			return
		}
		if w.adopted {
			if body == nil {
				w.T.Fatalf("the local Worker never answered: %s", stopped)
			}
			w.Pass("the local Worker answers, with no Cloudflare login: %.80q", body)
			return
		}
		if !allWork(body) {
			w.T.Fatalf("the local Worker: %s%s", body, stopped)
		}
		w.Pass("the local Worker's D1, KV, R2 and secret work, with no Cloudflare login")
	}},
	{Task: "cloudflare:set-secrets", Stage: int(deploy), Check: func(w *walker) {
		if len(w.secrets) == 0 {
			w.Pass("cloudflare:set-secrets: the Worker requires no secrets")
			return
		}
		if _, err := w.Task(nil, strings.Repeat("set-by-deploy\n", len(w.secrets)), "cloudflare:set-secrets"); err != nil {
			w.T.Fatalf("cloudflare:set-secrets: %v", err)
		}
		w.Pass("cloudflare:set-secrets set %s", strings.Join(w.secrets, ", "))
		w.miseShowSaysNext("mise run cloudflare:deploy")
	}},
	{Task: "cloudflare:deploy", Stage: int(deploy), Check: func(w *walker) {
		w.MustTask("cloudflare:deploy")
		s := w.miseShowJSON()
		if !s.LoggedIn || !s.Deployed || s.Profile != w.login {
			w.T.Fatalf("not deployed on the login %s: %+v", w.login, s)
		}
		w.url = s.URL
		if w.adopted {
			answers(w.T, w.url, 2*time.Minute)
			w.Pass("%s: deployed on the repo's own login, %s; it answers", w.url, s.Profile)
		} else {
			waitTillItWorks(w.T, w.url, 2*time.Minute)
			w.Pass("%s: deployed on the repo's own login, %s; its D1, KV, R2 and secret work", w.url, s.Profile)
		}
		if withExtras() {
			token, account := w.api()
			if on := w.extrasSettle(token, account, w.worker, true); len(on) != made() {
				w.T.Fatalf("wrangler deploy made %d of the %d extras it makes: %q", len(on), made(), on)
			} else {
				w.Pass("wrangler deploy made the extras too: %s", strings.Join(on, ", "))
			}
		}
		if env := withEnv(); env != "" {
			top := w.repo
			top.worker = w.login
			top.nothingOnCloudflare()
			w.Pass("deployed env.%s alone, as %s, on the repo's one login: nothing of the top level's Worker, %s", env, w.worker, w.login)
		}
		if changed := walk.Diff(w.before, w.Snapshot(local...)); len(changed) > 0 {
			w.T.Fatalf("deploying changed %s", strings.Join(changed, ", "))
		}
		w.Pass("deploying wrote nothing into the repo: no IDs")
		if remoteExtras() {
			if body, stopped := w.miseServeLocally(true); !allWork(body) {
				w.T.Fatalf("cloudflare:serve on the repo's login, the extras made: %s%s", body, stopped)
			}
			w.Pass("cloudflare:serve on the repo's login runs the extra wrangler runs on Cloudflare even locally")
		}
		if out, err := w.Task([]string{"CLOUDFLARE_API_TOKEN=dummy-not-a-real-token"}, "", "cloudflare:deploy"); err == nil || !strings.Contains(out, "refusing:") {
			w.T.Fatal("deployed with a token in the shell")
		}
		w.Pass("a stray CLOUDFLARE_API_TOKEN in the shell is refused")
		env := filepath.Join(w.Dir, ".env")
		os.WriteFile(env, []byte("CLOUDFLARE_API_TOKEN=dummy-not-a-real-token\n"), 0o600)
		out, err := w.Task(nil, "", "cloudflare:deploy")
		os.Remove(env)
		if err == nil || !strings.Contains(out, "refusing:") {
			w.T.Fatal("deployed with a token in .env")
		}
		w.Pass("a stray CLOUDFLARE_API_TOKEN in .env is refused")
		w.miseShowSaysNext("mise run cloudflare:connect-github")
	}},
	{Task: "cloudflare:connect-github", Stage: int(connectGithub), Check: func(w *walker) {
		w.MustTask("cloudflare:connect-github", "--branch", w.prod)
		w.Pass("Workers Builds deploys every push, production from %s", w.prod)
		// Again, with no flags, as the docs say a person can: it keeps what's set (an update, not a new connection).
		if out := w.MustTask("cloudflare:connect-github"); !strings.Contains(out, "a push to "+w.prod+" runs") {
			w.T.Fatalf("cloudflare:connect-github again didn't keep production on %s:\n%s", w.prod, out)
		}
		w.Pass("cloudflare:connect-github again keeps what's set: production from %s", w.prod)
		w.miseShowSaysNext("mise run cloudflare:set-preview-secrets")
	}},
	{Task: "cloudflare:set-preview-secrets", Stage: int(connectGithub), Check: func(w *walker) {
		if _, err := w.Task(nil, "set-by-deploy\n", "cloudflare:set-preview-secrets"); err != nil {
			w.T.Fatalf("cloudflare:set-preview-secrets: %v", err)
		}
		w.miseShowSaysNext("push a branch")

		w.Heading("A push to %s: Cloudflare builds and deploys production", w.prod)
		w.mustIn(w.Top, "git", "push", "-q", "origin", "HEAD:refs/heads/"+w.prod)
		if outcome := w.buildOutcome(w.prod, 15*time.Minute); outcome != "success" {
			w.T.Fatalf("Cloudflare's build of %s: %s", w.prod, outcome)
		}
		waitTillItWorks(w.T, w.url, 2*time.Minute)
		w.Pass("Cloudflare built and deployed production from %s; it works", w.prod)

		w.Heading("A pull request's branch %s: its preview, with its own D1, KV, R2 and secret", w.prev)
		wt := filepath.Join(w.T.TempDir(), "wt")
		w.mustIn(w.Top, "git", "worktree", "add", "-q", "--detach", wt, "HEAD")
		w.T.Cleanup(func() { walk.Exec(w.Top, os.Environ(), "", "git", "worktree", "remove", "--force", wt) })
		rel, _ := filepath.Rel(w.Top, w.Dir)
		os.WriteFile(filepath.Join(wt, rel, "preview.txt"), []byte("a pull request's change\n"), 0o644)
		w.mustIn(wt, "git", "add", "-A")
		w.mustIn(wt, "git", "commit", "-qm", "prove: a pull request's change")
		w.mustIn(wt, "git", "push", "-q", "origin", "HEAD:refs/heads/"+w.prev)
		preview := strings.Replace(w.url, "https://", "https://"+previewAlias(w.prev)+"-", 1)
		waitTillItWorks(w.T, preview, 15*time.Minute)
		w.Pass("%s: built by Cloudflare from %s; its D1, KV, R2 and secret work", preview, w.prev)

		w.Heading("Deleting %s, as merging a pull request does: its preview goes with it", w.prev)
		w.mustIn(w.Top, "git", "push", "-q", "origin", "--delete", w.prev)
		if !gone(preview, 10*time.Minute) {
			w.T.Fatalf("%s still answers, 10 minutes after its branch went", preview)
		}
		w.Pass("%s is gone", preview)
	}},
	{Task: "cloudflare:preview", Stage: int(deploy), Check: func(w *walker) {
		if w.adopted {
			w.Pass("cloudflare:preview: an adopted Worker's previews aren't walked: they need a previews block and secrets of its developer's")
			return
		}
		if w.upTo < connectGithub && len(w.secrets) > 0 {
			// Without connect-github's steps, its previews have no secrets yet: cloudflare:set-preview-secrets first, as the docs say.
			if _, err := w.Task(nil, strings.Repeat("set-by-deploy\n", len(w.secrets)), "cloudflare:set-preview-secrets"); err != nil {
				w.T.Fatalf("cloudflare:set-preview-secrets: %v", err)
			}
			w.Pass("cloudflare:set-preview-secrets set the previews' %s, which a preview from here needs too", strings.Join(w.secrets, ", "))
		}
		w.MustTask("cloudflare:preview", "--name", "e2e")
		token, account := w.api()
		var env map[string]any
		w.preview, env = w.previewOn(token, account, "e2e")
		waitTillItWorks(w.T, w.preview, 5*time.Minute)
		w.Pass("%s: a preview from here, on the repo's login; its D1, KV, R2 and secret work", w.preview)
		if withExtras() {
			if on := w.extrasSettle(token, account, w.worker+"-previews", true); len(on) != made() {
				w.T.Fatalf("cloudflare:preview made %d of the previews' own %d extras: %q", len(on), made(), on)
			} else if wrong := w.previewBindsOwn(token, account, env); len(wrong) > 0 {
				w.T.Fatalf("the preview doesn't bind the previews' own: %s", strings.Join(wrong, "; "))
			} else {
				w.Pass("the previews' own extras, made and bound in the preview: %s", strings.Join(on, ", "))
			}
		}
	}},
	{Task: "cloudflare:login", Stage: int(serve), Check: func(w *walker) {
		if out, err := w.Task([]string{"CI=true"}, "", "cloudflare:login"); err == nil || !strings.Contains(out, "in CI there's no one to log in") {
			w.T.Fatalf("in CI, a login didn't stop at once: %s", out)
		}
		w.Pass("in CI, cloudflare:login stops at once: no one is there to click Allow")
		if w.upTo >= deploy {
			w.MustTask("cloudflare:login")
			w.Pass("cloudflare:login: the repo's own login, %s", w.login)
		}
	}},
	{Task: "cloudflare:delete", Stage: int(deploy), Check: func(w *walker) {
		if w.upTo >= connectGithub {
			w.MustTask("cloudflare:connect-github", "--off")
			w.Pass("Workers Builds is off")
			w.MustTask("cloudflare:connect-github", "--off")
			w.Pass("Workers Builds is off again: cf's 404 for a connection that's gone is taken as gone (cf.NotFound)")
			w.mustIn(w.Top, "git", "push", "-q", "origin", "--delete", w.prod)
			w.Pass("the branch %s is deleted from GitHub", w.prod)
		}
		w.MustTask("cloudflare:delete", "--yes")
		urls := []string{w.url}
		if w.preview != "" {
			urls = append(urls, w.preview)
		}
		for _, u := range urls {
			if !gone(u, time.Minute) {
				w.T.Fatalf("%s still answers", u)
			}
		}
		w.Pass("no longer answering: %s", strings.Join(urls, ", "))
		if left := w.leftOnCloudflare(); len(left) > 0 {
			w.T.Fatalf("left on the account: %s", strings.Join(left, ", "))
		}
		if withExtras() {
			w.Pass("no D1, KV, R2 or Durable Object left, nor the extras' Queue or Flagship app, nor the previews' own")
		} else {
			w.Pass("no D1, KV, R2 or Durable Object left")
		}
	}},
	{Task: "cloudflare:logout", Stage: int(deploy), Check: func(w *walker) {
		if !*logoutFlag {
			w.Pass("the login kept, so the next walk needs no Allow (-args -logout logs out)")
			return
		}
		w.MustTask("cloudflare:logout")
		if w.miseShowJSON().LoggedIn {
			w.T.Fatal("the login is still there")
		}
		w.Pass("the login is gone")
	}},
	{Task: "cloudflare:clean", Stage: int(serve), Check: func(w *walker) {
		w.MustTask("cloudflare:clean", "--yes")
		for _, p := range []string{".wrangler", ".dev.vars"} {
			if _, err := os.Stat(filepath.Join(w.Dir, p)); err == nil {
				w.T.Fatalf("%s is still there", p)
			}
		}
		w.Pass("no .wrangler/ or .dev.vars left")
	}},
}
