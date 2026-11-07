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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joeblew999/mise-check/miseconfig"

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
// them; init brings mise-cloudflare's parts to what this release writes, and adds mise-check's pin to one without it).
var theirs = map[string]bool{".gitignore": true, ".claude/settings.json": true, ".mise/config.toml": true}

// templateConfig is the template's .mise/config.toml: the two pins, mise-cloudflare's settings and their comments.
var templateConfig, _ = fs.ReadFile(templates.Tool, ".mise/config.toml")

// configTool is mise-cloudflare as its init keeps its parts of a repo's .mise/config.toml (mise-check's miseconfig,
// which every tool's init imports): its pin at this release (local, a checkout's build, so a repo runs exactly what
// set it up), its own name in minimum_release_age_excludes, use_versions_host = false, and its comment at the top.
func configTool() miseconfig.Tool {
	release := ""
	if version != "" {
		release = buildVersion()
	}
	return miseconfig.Tool{Name: self, Release: release, Template: templateConfig, Init: "mise run cloudflare:init"}
}

// initRepo writes the template's tool half into the repo, each file at its path there.
func initRepo(dir string) {
	must(fs.WalkDir(templates.Tool, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		to := filepath.Join(dir, filepath.FromSlash(p))
		if b, err := os.ReadFile(to); err == nil && theirs[p] {
			if p != ".mise/config.toml" || !keepOwn(to, b) {
				fmt.Printf("✓ %s is the repo's own: left as it is\n", p)
			}
			return nil
		}
		b, err := fs.ReadFile(templates.Tool, p)
		if err == nil && p == ".mise/config.toml" {
			b, err = configTool().Pinned()
		}
		if err == nil {
			writeFile(to, b)
		}
		if strings.HasSuffix(p, ".sh") {
			must(os.Chmod(to, 0o755))
		}
		return err
	}))
}

// keepOwn brings mise-cloudflare's parts of the repo's own .mise/config.toml, at path, its text b, to what this release
// writes (miseconfig), and adds mise-check's pin, at latest as the template has it, when it has none; the rest stays
// as it is. It says what it wrote, and reports whether it did.
func keepOwn(path string, b []byte) bool {
	out, wrote := configTool().Keep(b)
	if !miseCheckLine.Match(out) {
		line := `"` + miseCheck + `" = "latest"`
		if i := bytes.Index(out, []byte("[tools]\n")); i >= 0 {
			i += len("[tools]\n")
			out = slices.Concat(out[:i], []byte(line+"\n"), out[i:])
		} else {
			out = append(bytes.TrimRight(out, "\n"), "\n\n[tools]\n"+line+"\n"...)
		}
		wrote = append(wrote, "mise-check's pin added (cloudflare:update runs it), "+line)
	}
	if len(wrote) > 0 {
		must(os.WriteFile(path, out, 0o644))
	}
	for _, w := range wrote {
		fmt.Printf("wrote %s: %s\n", path, w)
	}
	return len(wrote) > 0
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

// stale names the files init wrote that this version would write differently, what the repo's own .mise/config.toml
// lacks of mise-cloudflare's parts (miseconfig) and mise-check's pin, and a .claude/settings.json of the repo's own
// that doesn't run the hook.
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
			out = append(out, p+" is out of date: run mise run cloudflare:init")
		}
		return nil
	})
	if b, err := os.ReadFile(filepath.Join(r.dir, ".mise", "config.toml")); err == nil {
		if !miseCheckLine.Match(b) {
			out = append(out, ".mise/config.toml doesn't pin "+miseCheck+", which cloudflare:update runs: run mise run cloudflare:init, which adds it")
		}
		out = append(out, configTool().Problems(b)...)
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
