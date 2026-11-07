package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/joeblew999/mise-check/miseconfig"
	"github.com/joeblew999/mise-check/toolfiles"

	"github.com/joeblew999/mise-cloudflare/templates"
)

// self is mise-cloudflare as a repo's mise pins it (.mise/config.toml, which init writes).
const self = "github:joeblew999/mise-cloudflare"

// initTask writes mise-cloudflare's files again, as every problem with them says.
const initTask = "mise run cloudflare:init"

// templateConfig is the template's .mise/config.toml: mise-cloudflare's pin, its settings and their comments.
var templateConfig, _ = fs.ReadFile(templates.Tool, ".mise/config.toml")

// configTool is mise-cloudflare as its init keeps its parts of a repo's .mise/config.toml (mise-check's miseconfig,
// which every tool's init imports): its pin at this release (local, a checkout's build, so a repo runs exactly what
// set it up), its own name in minimum_release_age_excludes, use_versions_host = false, and its comment at the top.
func configTool() miseconfig.Tool {
	release := ""
	if version != "" {
		release = buildVersion()
	}
	return miseconfig.Tool{Name: self, Release: release, Template: templateConfig, Init: initTask}
}

// toolFiles is the template's tool half as init writes it into a repo (mise-check's toolfiles): each file at its path
// there, and of the repo's own .mise/config.toml, .gitignore and .claude/settings.json only mise-cloudflare's parts.
func toolFiles() toolfiles.Tool {
	return toolfiles.Tool{Files: templates.Tool, Hook: templates.HookFile, Config: configTool()}
}

// initRepo writes the template's tool half into the repo at dir. mise-check's own files are its init's
// (toolfiles.MiseCheckInit, which init runs after it).
func initRepo(dir string) { must(toolFiles().Write(dir)) }

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

// stale is what init would write again in the repo (toolfiles): a file of the tool half missing or out of date, a
// .claude/settings.json that doesn't run the hook, a .gitignore without the template's lines, and mise-cloudflare's
// parts of .mise/config.toml.
func (r repo) stale(ctx context.Context) []string { return toolFiles().Problems(r.dir) }
