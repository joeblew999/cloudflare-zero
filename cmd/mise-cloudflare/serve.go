package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joeblew999/mise-cloudflare/internal/rules"
	"github.com/joeblew999/mise-cloudflare/internal/wrangler"
)

// serve runs the Worker locally (wrangler dev), with local D1, KV and R2, and the secrets' local values from .dev.vars,
// on the repo's own login (--profile), never another login on the machine; an older wrangler has no logins of a repo's
// own, and runs it with no --profile, which local bindings never use. Local bindings need no login. One
// wrangler runs on Cloudflare even here (wrangler's own pick: Workers AI, an AI Search namespace, one marked remote, …)
// needs the repo's login, which serve makes first, as deploy does, and its resource there. The package's own build runs
// first, when it has one: wrangler dev then runs what it wrote (Cloudflare's Vite plugin's config, say). wrangler dev
// itself names the required secrets it has no value for.
func (r repo) serve(ctx context.Context, args []string) {
	devVars(r.dir)
	remote := strings.Join(r.wc.Remote, ", ")
	if remote != "" {
		switch {
		case r.w.LoggedIn(ctx):
		case inCI():
			fail(errors.New(remote + ": wrangler runs it on Cloudflare even locally, which needs the repo's login, and in CI there's no one to log in"))
		default:
			fmt.Printf("· %s: wrangler runs it on Cloudflare even here, which needs the repo's login: logging in first\n", remote)
		}
		r.ensureLogin(ctx)
	}
	r.build(ctx)
	dev := r.w.Plain(ctx, append([]string{"dev"}, args...)...)
	if r.w.HasProfiles(ctx) {
		dev = r.w.Cmd(ctx, append([]string{"dev"}, args...)...)
	}
	if err := wrangler.Interactive(dev); err != nil {
		// Not when it was stopped (Ctrl-C).
		if code := exitCode(err); code != -1 && code != 130 && remote != "" {
			fmt.Printf("· %s runs on Cloudflare even here: if wrangler couldn't reach it above, its resource must be there first (mise run cloudflare:deploy makes the ones wrangler makes, an AI Search namespace among them; any other you make yourself), then this again\n", remote)
		}
		os.Exit(exitCode(err))
	}
}

// devVars makes .dev.vars from .dev.vars.example for wrangler dev when there's none, git-ignored first.
func devVars(dir string) {
	path := filepath.Join(dir, ".dev.vars")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, ".dev.vars.example"))
	if err != nil {
		return
	}
	must(rules.EnsureIgnored(dir, ".dev.vars"))
	must(os.WriteFile(path, b, 0o600))
	fmt.Println("made .dev.vars from .dev.vars.example (git-ignored): put the local values there")
}
