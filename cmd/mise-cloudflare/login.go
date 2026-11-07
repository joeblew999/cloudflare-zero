package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joeblew999/mise-cloudflare/internal/wrangler"
)

// login is the login command: the repo's login made if it isn't yet, and its account shown.
func (r repo) login(ctx context.Context, jsonOut bool) {
	who := r.ensureLogin(ctx)
	if jsonOut {
		emit(who)
		return
	}
	fmt.Printf("✓ %s: %s\n", r.w.Profile, accountNames(who))
}

// wanted is the scopes the repo's login holds: wrangler's own login's, which wrangler keeps for everything it does.
func (r repo) wanted(ctx context.Context) []string {
	scopes, err := r.w.Scopes(ctx)
	if err != nil {
		fail(err)
	}
	return scopes
}

// ensureLogin logs the repo in if it isn't yet, and again whenever the login's scopes differ from the repo's (a scope added
// or removed), then checks they match. It refuses a stray token first: one would override every login.
func (r repo) ensureLogin(ctx context.Context) wrangler.Who {
	r.refuseStrayTokens()
	if p := r.noProfiles(ctx); len(p) > 0 {
		fail(errors.New(p[0]))
	}
	want := r.wanted(ctx)
	device := wrangler.Device()
	if !r.w.LoggedIn(ctx) {
		must(r.w.Login(ctx, want, device))
	} else {
		must(r.w.Activate(ctx))
	}
	who, err := r.w.Whoami(ctx)
	if err != nil {
		fail(err)
	}
	add, drop := wrangler.Missing(want, who.Scopes), wrangler.Extra(want, who.Scopes)
	if len(add) == 0 && len(drop) == 0 {
		return who
	}
	msg := "This repo's scopes changed."
	if len(add) > 0 {
		msg += " Needs: " + strings.Join(add, " ") + "."
	}
	if len(drop) > 0 {
		msg += " No longer needs: " + strings.Join(drop, " ") + "."
	}
	fmt.Fprintln(os.Stderr, msg)
	must(r.w.Login(ctx, want, device))
	if who, err = r.w.Whoami(ctx); err != nil {
		fail(err)
	}
	if len(wrangler.Missing(want, who.Scopes))+len(wrangler.Extra(want, who.Scopes)) > 0 {
		fail(fmt.Errorf("the new login's scopes (%s) still don't match the repo's (%s)", strings.Join(who.Scopes, " "), strings.Join(want, " ")))
	}
	return who
}

// noProfiles is the problem when the repo's wrangler has no auth profiles, which the repo's login is: nothing that
// needs the login runs on it.
func (r repo) noProfiles(ctx context.Context) []string {
	if r.w.HasProfiles(ctx) {
		return nil
	}
	return []string{fmt.Sprintf("the repo's wrangler, %s, has no auth profiles (wrangler auth), which this repo's login is: move it to a newer one (mise run mise-check:update moves every pin), then this again", r.w.Version())}
}

// strayVars are what wrangler takes over any login: an API token, CLOUDFLARE_API_TOKEN or CF_API_TOKEN (its old name,
// which it still reads), or the Global API Key, CLOUDFLARE_API_KEY or CF_API_KEY.
var strayVars = []string{"CLOUDFLARE_API_TOKEN", "CF_API_TOKEN", "CLOUDFLARE_API_KEY", "CF_API_KEY"}

// strayInShell is the first of strayVars set in this shell, or "".
func strayInShell() string {
	for _, v := range strayVars {
		if os.Getenv(v) != "" {
			return v
		}
	}
	return ""
}

// refuseStrayTokens: a token or key wrangler reads (strayVars) beats every login, so outside CI one in the shell or in
// a .env is refused.
func (r repo) refuseStrayTokens() {
	if inCI() {
		return
	}
	if v := strayInShell(); v != "" {
		fail(fmt.Errorf("refusing: %s is set in this shell, and it would override this repo's own login (%s). Unset it and run again", v, r.w.Profile))
	}
	if files := tokenEnvFiles(r.dir); len(files) > 0 {
		fail(fmt.Errorf("refusing: %s sets a Cloudflare token or key (%s), and wrangler would use it instead of this repo's own login (%s). Remove it, or name it so wrangler doesn't read it, and run again", strings.Join(files, " "), strings.Join(strayVars, ", "), r.w.Profile))
	}
}

var tokenLine = regexp.MustCompile(`(?m)^(CLOUDFLARE|CF)_API_(TOKEN|KEY)=.`)

func tokenEnvFiles(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, ".env*"))
	out := []string{}
	for _, m := range matches {
		if b, err := os.ReadFile(m); err == nil && tokenLine.Match(b) {
			out = append(out, filepath.Base(m))
		}
	}
	return out
}

func accountNames(who wrangler.Who) string {
	var names []string
	for _, a := range who.Accounts {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}
