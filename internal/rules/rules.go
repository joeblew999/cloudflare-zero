// Package rules is mise-cloudflare check: what a repo must be so that its Cloudflare login and its secrets stay out
// of it. Each problem is one line naming the file.
package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
)

var (
	token    = regexp.MustCompile(`CLOUDFLARE_API_TOKEN\s*[=:]\s*["']?[A-Za-z0-9_-]{30,}`)
	login    = regexp.MustCompile(`\bwrangler\s+login\b`)
	exactPin = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// Check returns the repo's problems, one line each.
func Check(dir string, w wconfig.Config) ([]string, error) {
	problems := Previews(w, false)
	bad := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	// wrangler's own validation of the config: what it warned about reading it (an unexpected field, …).
	if w.Warnings != "" {
		bad("wrangler warns about %s:\n%s", filepath.Base(w.Path), w.Warnings)
	}

	files, err := gitFiles(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			continue // gone, a directory, or binary
		}
		if token.Match(b) {
			bad("%s holds a Cloudflare token", f)
		}
		if !strings.HasSuffix(f, ".md") && login.Match(b) {
			bad("%s calls the machine-wide wrangler login", f)
		}
	}
	// What holds local secrets or cf's login cache must not be committed, by the repo's own ignore files only (another
	// developer doesn't have this machine's global gitignore). Only one that's there counts: the steps that write one
	// (serve, connect-github) git-ignore it first.
	for _, f := range secretFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil && !ignored(dir, f) {
			bad("%s would be committed: git-ignore it (%s)", f, "it holds local secrets, or cf's login")
		}
	}

	// Everyone runs the same wrangler: the one package.json pins exactly, or the one its committed lockfile does.
	pin := wranglerPin(dir)
	if lock, install := Lockfile(dir); pin == "" {
		bad("no package.json here or above it depends on wrangler: add it (the tasks run the repo's own), then %s", install)
	} else if lock == "" && !exactPin.MatchString(pin) {
		bad("wrangler is %s in package.json, and no lockfile is committed to pin it: commit the one %s makes, or pin it exactly", pin, install)
	}
	return problems, nil
}

// Previews returns the problems with the previews block (none without one, unless need): a KV, D1 or R2 in it must name
// an existing resource. The bindings a preview lacks, wrangler preview names itself.
func Previews(w wconfig.Config, need bool) []string {
	if !w.Previews {
		if need {
			return []string{fmt.Sprintf("add \"previews\": {} to %s: a preview needs it (its KV, D1 and R2 it gets of its own)", filepath.Base(w.Path))}
		}
		return nil
	}
	var problems []string
	if unnamed := unnamedResources(w); len(unnamed) > 0 {
		problems = append(problems, fmt.Sprintf("%s's previews block has %s with no resource named, which wrangler preview sends as it is and Cloudflare refuses: leave it out, and mise run cloudflare:08-deploy gives previews their own, or name an existing one (a KV namespace's id, a D1's database_id, an R2 bucket's bucket_name)", filepath.Base(w.Path), strings.Join(unnamed, ", ")))
	}
	return problems
}

// unnamedResources names the previews block's KV, D1 and R2 (wconfig.PreviewsOwn) that don't name the resource they use:
// wrangler deploy makes those for production, wrangler preview makes none.
func unnamedResources(w wconfig.Config) []string {
	var out []string
	for _, n := range slices.Sorted(maps.Keys(w.PreviewBindings)) {
		if kind, own := wconfig.PreviewsOwn[w.PreviewBindings[n]]; own && !slices.Contains(w.PreviewNamed, n) {
			out = append(out, kind+" "+n)
		}
	}
	return out
}

// secretFiles is what holds local secrets (.dev.vars, .env) or cf's login cache (.cloudflare/: the account it logged
// into, its ID and name).
var secretFiles = []string{".dev.vars", ".env", ".cloudflare/"}

func ignored(dir, f string) bool {
	return exec.Command("git", "-C", dir, "-c", "core.excludesFile=", "check-ignore", "-q", f).Run() == nil
}

// EnsureIgnored git-ignores f in dir's own .gitignore, before a step writes it, unless the repo already does.
func EnsureIgnored(dir, f string) error {
	if ignored(dir, f) {
		return nil
	}
	path := filepath.Join(dir, ".gitignore")
	b, _ := os.ReadFile(path)
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	if err := os.WriteFile(path, append(b, f+"\n"...), 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ %s is git-ignored now (added to .gitignore): it holds local secrets, or cf's login\n", f)
	return nil
}

// lockfiles is each package manager's lockfile and how Workers Builds installs from it. aube's own, aube-lock.yaml,
// comes first: aube reads and writes every other kind in place, and a repo that has its own uses aube.
var lockfiles = []struct{ file, install string }{
	{"aube-lock.yaml", "aube install --frozen-lockfile"},
	{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
	{"bun.lock", "bun install --frozen-lockfile"},
	{"bun.lockb", "bun install --frozen-lockfile"},
	{"yarn.lock", "yarn install --frozen-lockfile"},
	{"package-lock.json", "npm ci"},
}

// Lockfile is the lockfile committed for dir, in it or in a folder above it in its git repo (a workspace's), and the
// command that installs from it: "" and npm install when there's none.
func Lockfile(dir string) (file, install string) {
	for d := range upward(dir) {
		for _, l := range lockfiles {
			if exec.Command("git", "-C", d, "ls-files", "--error-unmatch", l.file).Run() == nil {
				return filepath.Join(d, l.file), l.install
			}
		}
	}
	return "", "npm install"
}

// Build is the command that runs the package's own build script (package.json's "build", in dir), with the package
// manager its lockfile is for (npm run build, pnpm run build, …), or "" when it has none. A Worker built first
// (Cloudflare's Vite plugin writes the config wrangler then deploys, its own deploy script being "npm run build &&
// wrangler deploy") is run, deployed and previewed after it.
func Build(dir string) string {
	var pkg struct{ Scripts map[string]string }
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err != nil || json.Unmarshal(b, &pkg) != nil || pkg.Scripts["build"] == "" {
		return ""
	}
	_, install := Lockfile(dir)
	return strings.Fields(install)[0] + " run build"
}

// wranglerPin is the wrangler version the nearest package.json at or above dir, in its git repo, depends on, or "".
func wranglerPin(dir string) string {
	for d := range upward(dir) {
		var pkg struct{ Dependencies, DevDependencies map[string]string }
		if b, err := os.ReadFile(filepath.Join(d, "package.json")); err == nil && json.Unmarshal(b, &pkg) == nil {
			if v := cmpOr(pkg.DevDependencies["wrangler"], pkg.Dependencies["wrangler"]); v != "" {
				return v
			}
		}
	}
	return ""
}

// upward is dir and each folder above it, up to its git repo's top (dir alone outside one).
func upward(dir string) func(func(string) bool) {
	return func(yield func(string) bool) {
		top, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
		real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return filepath.Clean(r) }
		stop := real(strings.TrimSpace(string(top)))
		for d := dir; ; d = filepath.Dir(d) {
			if !yield(d) || err != nil || real(d) == stop || d == filepath.Dir(d) {
				return
			}
		}
	}
}

// gitFiles is the repo's files under dir, tracked or not, without the git-ignored ones.
func gitFiles(dir string) ([]string, error) {
	out, err := exec.Command("git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", ".").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w (the rules need a git repo)", dir, err)
	}
	var files []string
	for _, f := range strings.Fields(string(out)) {
		if !strings.HasPrefix(f, "node_modules/") && !strings.Contains(f, "/node_modules/") {
			files = append(files, f) // installed packages are not the repo's, git-ignored or not
		}
	}
	return files, nil
}

func cmpOr(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
