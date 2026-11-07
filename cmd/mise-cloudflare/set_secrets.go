package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"
)

// setSecrets sets each secret the Worker requires (its config's secrets.required) that Cloudflare doesn't have yet,
// asking for its value; with previews, each one the Worker's Previews Base doesn't have, which every preview starts
// from (previews don't inherit the Worker's). A value is typed unseen on a terminal, else read a line each from stdin,
// all of it read first, before any wrangler runs: one that inherits stdin would take the values otherwise.
func (r repo) setSecrets(ctx context.Context, previews bool) {
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	var piped []string
	if !tty {
		piped = pipedLines(os.Stdin)
	}
	who := r.ensureLogin(ctx)
	where, whose, put := r.wc.Name, r.wc.Name+"'s", []string{"secret", "put"}
	var missing []string
	if previews {
		if !r.deployed(ctx) {
			fail(errors.New("previews' secrets go in the Worker's Previews Base: deploy it first (mise run cloudflare:deploy)"))
		}
		if len(who.Accounts) != 1 {
			fail(fmt.Errorf("the login %s can use %d accounts; log it in again and pick one", r.w.Profile, len(who.Accounts)))
		}
		token, err := r.w.Token(ctx)
		must(err)
		_, missing = r.previewsMissing(ctx, token, who.Accounts[0].ID)
		where, whose, put = r.wc.Name+"'s previews", r.wc.Name+"'s previews'", []string{"preview", "base-config", "secret", "put"}
	} else {
		// A Worker that isn't on Cloudflare yet has none: wrangler secret put makes it.
		var have []struct{ Name string }
		r.wranglerJSON(ctx, &have, "secret", "list")
		for _, s := range r.wc.Secrets {
			if !slices.ContainsFunc(have, func(h struct{ Name string }) bool { return h.Name == s }) {
				missing = append(missing, s)
			}
		}
	}
	if len(missing) == 0 {
		fmt.Printf("✓ %s has every secret it requires\n", where)
		return
	}
	for _, name := range missing {
		var value string
		if tty {
			fmt.Fprintf(os.Stderr, "%s %s (hidden): ", whose, name)
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			must(err)
			value = string(b)
		} else if len(piped) > 0 {
			value, piped = piped[0], piped[1:]
		}
		if value == "" {
			fail(fmt.Errorf("no value for %s %s", whose, name))
		}
		cmd := r.w.Cmd(ctx, append(slices.Clone(put), name)...)
		cmd.Stdin = strings.NewReader(value)
		if out, err := cmd.CombinedOutput(); err != nil {
			fail(fmt.Errorf("wrangler %s %s: %w\n%s", strings.Join(put, " "), name, err, out))
		}
		fmt.Printf("✓ %s %s is set\n", whose, name)
	}
}

// pipedLines are the lines piped to r, each without its line ending.
func pipedLines(r io.Reader) []string {
	b, _ := io.ReadAll(r)
	var lines []string
	for line := range strings.Lines(string(b)) {
		lines = append(lines, strings.TrimRight(line, "\r\n"))
	}
	return lines
}
