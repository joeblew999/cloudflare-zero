package rules

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skip("no git")
	}
	for name, body := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	return dir
}

func good() map[string]string {
	return map[string]string{
		".dev.vars.example": "GREETING=dummy\n",
		".gitignore":        ".dev.vars\n.env*\n.cloudflare/\nnode_modules/\n",
		"package.json":      `{ "devDependencies": { "wrangler": "4.151.3" } }`,
		"README.md":         "Never run wrangler login here.\n",
		"src/worker.js":     "export default {}\n",
		"notes.txt":         "this machine's other wrangler logins\n",
	}
}

// cfg is what wrangler reads from a config with one D1 and one KV that name no resource, and a previews block whose
// D1 and KV name staging's, needing GREETING.
func cfg() wconfig.Config {
	return wconfig.Config{Path: "/r/wrangler.jsonc", Name: "w", Secrets: []string{"GREETING"},
		Bindings: map[string]string{"DB": "d1", "KV": "kv_namespace"}, PreviewBindings: map[string]string{"DB": "d1", "KV": "kv_namespace"},
		PreviewNamed: []string{"DB", "KV"}, Previews: true}
}

func TestCheckPasses(t *testing.T) {
	if p, err := Check(repo(t, good()), cfg()); err != nil || len(p) != 0 {
		t.Errorf("problems %q, %v", p, err)
	}
}

func TestCheckFinds(t *testing.T) {
	files := good()
	files["deploy.sh"] = "npx wrangler login\n"
	files["notes.txt"] = "CLOUDFLARE_API_TOKEN=abcdefghijklmnopqrstuvwxyz0123456789\n"
	files["package.json"] = `{ "devDependencies": { "wrangler": "^4.0.0" } }`
	files[".gitignore"] = "node_modules/\n"
	files[".dev.vars"] = "GREETING=local\n"
	files[".cloudflare/account.json"] = "{}\n"
	c := cfg()
	c.Secrets = []string{"GREETING", "KEY"}
	c.Previews, c.PreviewBindings, c.PreviewNamed = false, nil, nil
	c.Warnings = `▲ [WARNING] Processing wrangler.jsonc configuration:
    - Unexpected fields found in top-level field: "kv_namespacez"`
	p, err := Check(repo(t, files), c)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(p, "\n")
	for _, want := range []string{"wrangler warns about wrangler.jsonc:", `"kv_namespacez"`, "deploy.sh calls",
		"notes.txt holds a Cloudflare token", ".dev.vars would be committed", ".cloudflare/ would be committed", "no lockfile is committed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, ".env would") {
		t.Errorf("a .env that isn't there isn't a problem:\n%s", got)
	}
	if strings.Contains(got, "README.md") {
		t.Errorf("a .md file may mention wrangler login:\n%s", got)
	}
}

// TestPreviewBindings: a binding the previews block doesn't have isn't check's to name: wrangler preview names the ones
// a preview lacks itself.
func TestPreviewBindings(t *testing.T) {
	c := cfg()
	c.PreviewBindings = map[string]string{"AI": "ai"}
	c.Bindings = map[string]string{"DB": "d1", "KV": "kv_namespace", "BUCKET": "r2_bucket", "INDEX": "vectorize", "JOBS": "queue", "AI": "ai", "ASSETS": "assets"}
	if p, err := Check(repo(t, good()), c); err != nil || len(p) != 0 {
		t.Errorf("problems %q, %v", p, err)
	}
}

// TestPreviewResources: a previews block's KV, D1 or R2 that doesn't name its resource is named: wrangler preview
// creates none.
func TestPreviewResources(t *testing.T) {
	c := cfg()
	c.Bindings["BUCKET"] = "r2_bucket"
	c.PreviewBindings["BUCKET"] = "r2_bucket"
	c.PreviewNamed = []string{"DB"}
	p := Previews(c, false)
	if len(p) != 1 || !strings.Contains(p[0], "has R2 BUCKET, KV KV with no resource named") {
		t.Errorf("problems %q", p)
	}
}

// TestPreviewsNeeded: no previews block is fine for a repo that runs no Worker Previews, a problem for one that does.
func TestPreviewsNeeded(t *testing.T) {
	c := cfg()
	c.Previews = false
	if p := Previews(c, false); len(p) != 0 {
		t.Errorf("problems %q", p)
	}
	if p := Previews(c, true); len(p) != 1 || !strings.Contains(p[0], "add \"previews\": {}") {
		t.Errorf("problems %q", p)
	}
}

// TestLockfile: a committed lockfile pins wrangler, in the folder or a workspace's above it; an uncommitted one doesn't
// (Workers Builds checks the repository out), and the install is then npm install.
func TestLockfile(t *testing.T) {
	dir := repo(t, map[string]string{"package-lock.json": "{}", "worker/package.json": `{ "devDependencies": { "wrangler": "^4.0.0" } }`})
	worker := filepath.Join(dir, "worker")
	if f, install := Lockfile(worker); f != "" || install != "npm install" {
		t.Errorf("an uncommitted package-lock.json: %q, %q", f, install)
	}
	exec.Command("git", "-C", dir, "add", "package-lock.json").Run()
	if f, install := Lockfile(worker); filepath.Base(f) != "package-lock.json" || install != "npm ci" {
		t.Errorf("a workspace's committed package-lock.json: %q, %q", f, install)
	}
	if p, err := Check(worker, cfg()); err != nil || strings.Contains(strings.Join(p, "\n"), "wrangler is") {
		t.Errorf("a ^ range with a committed lockfile is pinned: %q, %v", p, err)
	}
	aube := repo(t, map[string]string{"aube-lock.yaml": "lockfileVersion: '9.0'\n", "package.json": `{ "devDependencies": { "wrangler": "^4.0.0" } }`})
	exec.Command("git", "-C", aube, "add", "aube-lock.yaml").Run()
	if f, install := Lockfile(aube); filepath.Base(f) != "aube-lock.yaml" || install != "aube install --frozen-lockfile" {
		t.Errorf("a committed aube-lock.yaml: %q, %q", f, install)
	}
}

// TestInBuildImage: aube, which Workers Builds' image doesn't have, runs there from npm, at the repo's pin; the other
// package managers are there already.
func TestInBuildImage(t *testing.T) {
	for _, tc := range []struct{ cmd, version, want string }{
		{"aube install --frozen-lockfile && aube run build", "2.9.0", "npx -y --ignore-scripts=false @endevco/aube@2.9.0 install --frozen-lockfile && npx -y --ignore-scripts=false @endevco/aube@2.9.0 run build"},
		{"aube install --frozen-lockfile", "", "npx -y --ignore-scripts=false @endevco/aube install --frozen-lockfile"},
		{"pnpm install --frozen-lockfile && pnpm run build", "2.9.0", "pnpm install --frozen-lockfile && pnpm run build"},
		{"npm ci", "", "npm ci"},
	} {
		if got := InBuildImage(tc.cmd, tc.version); got != tc.want {
			t.Errorf("InBuildImage(%q, %q) = %q, want %q", tc.cmd, tc.version, got, tc.want)
		}
	}
}

// TestBuild: a package with a build script is built with its package manager first; one with none, or no package, isn't.
func TestBuild(t *testing.T) {
	dir := t.TempDir()
	if got := Build(dir); got != "" {
		t.Errorf("no package.json: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts": {"dev": "wrangler dev"}}`), 0o644)
	if got := Build(dir); got != "" {
		t.Errorf("no build script: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts": {"build": "vite build", "deploy": "npm run build && wrangler deploy"}}`), 0o644)
	if got := Build(dir); got != "npm run build" {
		t.Errorf("a build script: %q, want npm run build", got)
	}
	aube := repo(t, map[string]string{"aube-lock.yaml": "lockfileVersion: '9.0'\n", "package.json": `{"scripts": {"build": "hugo"}}`})
	exec.Command("git", "-C", aube, "add", "aube-lock.yaml").Run()
	if got := Build(aube); got != "aube run build" {
		t.Errorf("a build script with aube-lock.yaml: %q, want aube run build", got)
	}
}
