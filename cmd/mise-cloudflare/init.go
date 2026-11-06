package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joeblew999/mise-cloudflare/templates"
)

// self is mise-cloudflare as a repo's mise pins it (.mise/config.toml, which init writes).
const self = "github:joeblew999/mise-cloudflare"

// miseCheck is mise-check as a repo's mise pins it, beside mise-cloudflare in .mise/config.toml: cloudflare:update runs
// mise-check update, which moves every pin, these two among them.
const miseCheck = "github:joeblew999/mise-check"

// miseCheckLine is mise-check's pin in .mise/config.toml.
var miseCheckLine = regexp.MustCompile(regexp.QuoteMeta(`"`+miseCheck+`" = "`) + `[^"]*"`)

// theirs are the template's files a repo may have of its own already: init writes them only when it has none.
// .mise/config.toml is mise-cloudflare's pin and mise-check's: written once, then the repo's (cloudflare:update moves
// them, init adds mise-check's to one that has none, and a
// newer release's init, run by hand: forward only).
var theirs = map[string]bool{".gitignore": true, ".claude/settings.json": true, ".mise/config.toml": true}

// pinLine is mise-cloudflare's pin in .mise/config.toml.
var pinLine = regexp.MustCompile(regexp.QuoteMeta(`"`+self+`" = "`) + `[^"]*"`)

// pinned is the template's .mise/config.toml pinning this build: a release pins itself, a build of a checkout local
// (mise run tool:install there links it), so a repo runs exactly what set it up.
func pinned(b []byte) []byte {
	pin := "local"
	if version != "" {
		pin = strings.TrimPrefix(version, "v")
	}
	if !pinLine.Match(b) {
		fail(fmt.Errorf("the template's .mise/config.toml doesn't pin %s", self))
	}
	return pinLine.ReplaceAll(b, []byte(`"`+self+`" = "`+pin+`"`))
}

// initRepo writes the template's tool half into the repo, each file at its path there.
func initRepo(dir string) {
	must(fs.WalkDir(templates.Tool, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		to := filepath.Join(dir, filepath.FromSlash(p))
		if b, err := os.ReadFile(to); err == nil && theirs[p] {
			if p == ".mise/config.toml" {
				added := addMiseCheck(to, b)
				if b, _ = os.ReadFile(to); movePin(to, b) || added {
					return nil
				}
			}
			fmt.Printf("✓ %s is the repo's own: left as it is\n", p)
			return nil
		}
		b, err := fs.ReadFile(templates.Tool, p)
		if err == nil {
			if p == ".mise/config.toml" {
				b = pinned(b)
			}
			writeFile(to, b)
		}
		if strings.HasSuffix(p, ".sh") {
			must(os.Chmod(to, 0o755))
		}
		return err
	}))
}

// movePin moves the repo's pin of mise-cloudflare in its .mise/config.toml (b) forward to this release, when it pins
// an older one: init at a newer release, run by hand, is the move to it. The rest of the file stays as it is; a pin
// of local, or of this release or a newer one, stays too, as does every pin when this is a checkout's build.
func movePin(path string, b []byte) bool {
	m := pinLine.Find(b)
	at := strings.TrimSuffix(strings.TrimPrefix(string(m), `"`+self+`" = "`), `"`)
	if version == "" || m == nil || at == "local" || !older(at, buildVersion()) {
		return false
	}
	must(os.WriteFile(path, pinLine.ReplaceAll(b, []byte(`"`+self+`" = "`+buildVersion()+`"`)), 0o644))
	fmt.Printf("wrote %s: mise-cloudflare %s → %s\n", path, at, buildVersion())
	return true
}

// addMiseCheck adds mise-check's pin, at latest, to the repo's own .mise/config.toml (b) when it has none, as one
// written before cloudflare:update ran mise-check: under its [tools]. The rest of the file stays as it is.
func addMiseCheck(path string, b []byte) bool {
	if miseCheckLine.Match(b) {
		return false
	}
	line := `"` + miseCheck + `" = "latest"`
	s := string(b)
	if i := strings.Index(s, "[tools]\n"); i >= 0 {
		s = s[:i+len("[tools]\n")] + line + "\n" + s[i+len("[tools]\n"):]
	} else {
		s = strings.TrimRight(s, "\n") + "\n\n[tools]\n" + line + "\n"
	}
	must(os.WriteFile(path, []byte(s), 0o644))
	fmt.Printf("wrote %s: mise-check's pin added (cloudflare:update runs it), %s\n", path, line)
	return true
}

// older reports whether release a is older than release b, both x.y.z.
func older(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, errx := strconv.Atoi(pa[i])
		y, erry := strconv.Atoi(pb[i])
		if errx != nil || erry != nil {
			return false
		}
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// newestRelease is mise-cloudflare's newest release, as GitHub's page for it names it (…/releases/latest redirects to
// its tag), or "" if GitHub can't say.
func newestRelease() string {
	c := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get("https://github.com/joeblew999/mise-cloudflare/releases/latest")
	if err != nil {
		return ""
	}
	resp.Body.Close()
	_, tag, _ := strings.Cut(resp.Header.Get("Location"), "/releases/tag/")
	return strings.TrimPrefix(tag, "v")
}

// sayNewest says, of a release, when GitHub has a newer one, and how to set the repo up at it: mise takes a release as
// latest only once it's a day old, so @latest can run an older one than the newest.
func sayNewest() {
	if version == "" {
		return
	}
	switch n := newestRelease(); {
	case n == "":
		fmt.Printf("· this is mise-cloudflare %s; GitHub didn't say what its newest release is\n", buildVersion())
	case older(buildVersion(), n):
		fmt.Printf("· this is mise-cloudflare %s, and the newest release is %s: mise takes a release as latest only once it's a day old. For it, which moves the repo's pin to it:\n  mise exec %s@%s -- mise-cloudflare init\n", buildVersion(), n, self, n)
	default:
		fmt.Printf("✓ mise-cloudflare %s, the newest release\n", buildVersion())
	}
}

// stale names the files init wrote that this version would write differently, and a .claude/settings.json of the
// repo's own that doesn't run the hook.
func (r repo) stale(ctx context.Context) []string {
	var out []string
	fs.WalkDir(templates.Tool, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || theirs[p] {
			return err
		}
		want, _ := fs.ReadFile(templates.Tool, p)
		// Line endings aside: a Windows checkout of the repo may have \r\n where init wrote \n.
		crlf := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
		if b, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(p))); err == nil && !bytes.Equal(crlf(b), crlf(want)) {
			out = append(out, p+" is out of date: run mise run cloudflare:03-init")
		}
		return nil
	})
	if b, err := os.ReadFile(filepath.Join(r.dir, ".mise", "config.toml")); err == nil && !miseCheckLine.Match(b) {
		out = append(out, ".mise/config.toml doesn't pin "+miseCheck+", which cloudflare:update runs: run mise run cloudflare:03-init, which adds it")
	}
	if b, err := os.ReadFile(filepath.Join(r.dir, ".claude", "settings.json")); err == nil && !bytes.Contains(b, []byte(templates.HookFile)) {
		out = append(out, ".claude/settings.json doesn't run .claude/hooks/"+templates.HookFile+": add its SessionStart entry (docs/mise-cloudflare/README.md)")
	}
	return out
}

func writeFile(path string, b []byte) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b) {
		fmt.Printf("✓ %s is up to date\n", path)
		return
	}
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	must(os.WriteFile(path, b, 0o644))
	fmt.Println("wrote", path)
}
