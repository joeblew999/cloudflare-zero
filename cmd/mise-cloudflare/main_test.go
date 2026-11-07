package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
	"github.com/joeblew999/mise-cloudflare/templates"
)

// commands is every mise-cloudflare command, in the order of a repo's tasks: the steps, init to clean, then the rest.
var commands = []string{"init", "check", "show", "serve", "set-secrets", "deploy", "connect-github", "set-preview-secrets", "preview", "login", "delete", "logout", "clean", "verify", "template", "run-wrangler", "show-version", "show-help"}

// taskVerb is the command a task's name names: cloudflare:deploy names deploy.
var taskVerb = regexp.MustCompile(`^cloudflare:([a-z][a-z-]*)$`)

// TestSkill: the skill installs where Claude Code finds it, and names its tasks right.
func TestSkill(t *testing.T) {
	b, err := fs.ReadFile(templates.Tool, ".claude/skills/cloudflare/SKILL.md")
	if err != nil || !regexp.MustCompile(`(?s)^---\nname: cloudflare\ndescription: .+\n---\n`).Match(b) {
		t.Fatalf("SKILL.md: %v\n%.200s", err, b)
	}
	for _, m := range regexp.MustCompile(`mise run (cloudflare:[a-z0-9][a-z0-9-]*)`).FindAllStringSubmatch(string(b), -1) {
		if v := taskVerb.FindStringSubmatch(m[1]); v == nil || !slices.Contains(commands, v[1]) {
			t.Errorf("the skill names %s, which isn't a command's task", m[1])
		}
	}
	if m := regexp.MustCompile(`mise run cloudflare [a-z]\w*`).FindString(string(b)); m != "" {
		t.Errorf("the skill names %q: each command is its task, mise run cloudflare:<command>", m)
	}
}

// TestTokenEnvFiles: a .env or .env.* setting a token or key wrangler reads is found, under its old name too
// (CF_API_TOKEN); an empty one, a comment, or one named so wrangler doesn't read it isn't.
func TestTokenEnvFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\nCLOUDFLARE_API_TOKEN=abc\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("CLOUDFLARE_API_TOKEN=\n# CLOUDFLARE_API_TOKEN=x\nMY_CF_TOKEN=x\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.old"), []byte("CF_API_TOKEN=abc\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.key"), []byte("CLOUDFLARE_API_KEY=abc\n"), 0o600)
	if got := tokenEnvFiles(dir); !slices.Equal(got, []string{".env", ".env.key", ".env.old"}) {
		t.Errorf("got %q", got)
	}
}

// TestStrayInShell: each token or key wrangler reads, set in the shell, is named; none set, none is.
func TestStrayInShell(t *testing.T) {
	for _, v := range strayVars {
		t.Setenv(v, "")
	}
	if got := strayInShell(); got != "" {
		t.Errorf("none set: %q", got)
	}
	t.Setenv("CF_API_TOKEN", "x")
	if got := strayInShell(); got != "CF_API_TOKEN" {
		t.Errorf("CF_API_TOKEN set: %q", got)
	}
}

// TestPipedLines: piped values are read whole, a line each, without their line endings (\r\n too), a last line with none
// included.
func TestPipedLines(t *testing.T) {
	if got := pipedLines(strings.NewReader("one\r\ntwo\nthree")); !slices.Equal(got, []string{"one", "two", "three"}) {
		t.Errorf("got %q", got)
	}
	if got := pipedLines(strings.NewReader("")); len(got) != 0 {
		t.Errorf("nothing piped: %q", got)
	}
}

// TestCI: CI=true is CI; unset, false or 0 isn't.
func TestCI(t *testing.T) {
	for v, want := range map[string]bool{"true": true, "1": true, "": false, "false": false, "0": false} {
		t.Setenv("CI", v)
		if inCI() != want {
			t.Errorf("CI=%q: %v", v, !want)
		}
	}
}

// TestTemplate: template writes the template's Worker, named for the folder, into a repo with none, and never writes
// over a file: a repo with a Worker of its own gets none of it (a README, if it has none), and a mise.toml or
// README.md of its own is left as it is.
func TestTemplate(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "My_New Repo")
	os.Mkdir(empty, 0o755)
	templateRepo(empty)
	for _, f := range []string{"src/worker.js", "wrangler.jsonc", "package.json", "pnpm-lock.yaml", ".dev.vars.example", "mise.toml", "README.md"} {
		if _, err := os.Stat(filepath.Join(empty, f)); err != nil {
			t.Errorf("%s not written into an empty repo: %v", f, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(empty, "README.md")); !bytes.HasPrefix(b, []byte("# my-new-repo\n")) {
		t.Errorf("README.md isn't named for its folder, my-new-repo:\n%s", b)
	}
	for _, f := range []string{"wrangler.jsonc", "package.json"} {
		if b, _ := os.ReadFile(filepath.Join(empty, f)); !bytes.Contains(b, []byte(`"my-new-repo"`)) || bytes.Contains(b, []byte(templates.WorkerName)) {
			t.Errorf("%s isn't named for its folder, my-new-repo:\n%s", f, b)
		}
	}
	own := t.TempDir()
	os.WriteFile(filepath.Join(own, "package.json"), []byte(`{"name":"mine"}`), 0o644)
	templateRepo(own)
	if entries, _ := os.ReadDir(own); len(entries) != 2 {
		t.Errorf("a repo with a package.json of its own got more than a README: %v", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(own, "package.json")); string(b) != `{"name":"mine"}` {
		t.Errorf("the repo's own package.json was written over: %q", b)
	}
	os.WriteFile(filepath.Join(own, "README.md"), []byte("mine\n"), 0o644)
	templateRepo(own)
	if b, _ := os.ReadFile(filepath.Join(own, "README.md")); string(b) != "mine\n" {
		t.Errorf("the repo's own README.md was written over: %q", b)
	}
	tools := t.TempDir()
	mine := "[tools]\njq = \"1.8.2\"\n"
	os.WriteFile(filepath.Join(tools, "mise.toml"), []byte(mine), 0o644)
	templateRepo(tools)
	if b, _ := os.ReadFile(filepath.Join(tools, "mise.toml")); string(b) != mine {
		t.Errorf("the repo's own mise.toml was written over: %q", b)
	}
	if _, err := os.Stat(filepath.Join(tools, "wrangler.jsonc")); err != nil {
		t.Errorf("no Worker written beside a mise.toml of the repo's own: %v", err)
	}
	for _, bad := range []string{"___", strings.Repeat("a", 64)} {
		if _, err := workerName(bad); err == nil {
			t.Errorf("%q makes a Worker name", bad)
		}
	}
}

// TestLastDeployURL: the URL is the last deploy entry's in wrangler's output file (one JSON entry a line), its
// workers.dev target first; anything else in the file is wrangler's, not a deploy.
func TestLastDeployURL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".wrangler"), 0o755)
	out := `{"type":"wrangler-session","version":1,"wrangler_version":"4.155.0"}
{"type":"deploy","version":1,"worker_name":"w","targets":["https://old.example.workers.dev"]}
{"type":"deploy","version":1,"worker_name":"w","targets":["https://example.com","https://w.example.workers.dev"]}
`
	os.WriteFile(filepath.Join(dir, ".wrangler", "output.json"), []byte(out), 0o644)
	if got := lastDeployURL(dir); got != "https://w.example.workers.dev" {
		t.Errorf("lastDeployURL: %q", got)
	}
	if got := lastDeployURL(t.TempDir()); got != "" {
		t.Errorf("no output file: %q", got)
	}
}

// TestOwnResources: delete's list is only the KV, D1 and R2 that name no resource (wrangler deploy made them, as
// <worker>-<binding>); one the config names is the developer's, kept; other kinds aren't resources deploy makes.
func TestOwnResources(t *testing.T) {
	r := repo{wc: wconfig.Config{Name: "w", Named: []string{"CACHE", "HYPERDRIVE"},
		Bindings: map[string]string{"CACHE": "kv_namespace", "KV": "kv_namespace", "DB": "d1", "HYPERDRIVE": "hyperdrive", "LIMITER": "durable_object_namespace", "JOBS": "queue",
			"FLAGS": "flagship", "SEARCH": "ai_search_namespace"}}}
	own, kept := r.ownResources()
	var names []string
	for _, p := range own {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"w-db", "w-flags", "w-jobs", "w-kv"}) || !slices.Equal(kept, []string{"KV CACHE"}) {
		t.Errorf("own %q, kept %q", names, kept)
	}
}

// TestPreviewNeeds: previews get their own of every kind wrangler deploy makes and delete deletes (KV, D1, R2, a Queue,
// a Flagship app), each <worker>-previews-<binding>, unless the previews block declares the binding itself; nothing of
// another kind.
func TestPreviewNeeds(t *testing.T) {
	r := repo{wc: wconfig.Config{Name: "w", PreviewBindings: map[string]string{"CACHE": "kv_namespace"},
		Bindings: map[string]string{"CACHE": "kv_namespace", "DB": "d1", "BUCKET": "r2_bucket", "JOBS": "queue", "FLAGS": "flagship",
			"PG": "hyperdrive", "AI": "ai"}}}
	var got []string
	for _, p := range r.previewNeeds() {
		got = append(got, p.Kind+" "+p.Name)
	}
	if want := []string{"R2 w-previews-bucket", "D1 w-previews-db", "Flagship w-previews-flags", "Queue w-previews-jobs"}; !slices.Equal(got, want) {
		t.Errorf("previewNeeds: %q, want %q", got, want)
	}
}

// TestDeleting: delete's question names what goes besides the Worker: its own Durable Objects, and what deploy and the
// previews made, by name. A Worker with none of them is asked about alone.
func TestDeleting(t *testing.T) {
	r := repo{wc: wconfig.Config{Name: "w", Bindings: map[string]string{"KV": "kv_namespace", "PG": "hyperdrive"}, Previews: true}}
	if got, want := r.deleting([]string{"Room"}), ", with its Durable Objects (Room), KV w-kv, KV "+previewName("w", "KV")+" and all their data"; got != want {
		t.Errorf("deleting: %q, want %q", got, want)
	}
	if got := (repo{wc: wconfig.Config{Name: "w", Bindings: map[string]string{"MODE": "plain_text"}}}).deleting(nil); got != "" {
		t.Errorf("a Worker with nothing besides: %q", got)
	}
}

// TestNotDeleted: delete names every binding it leaves, whatever its kind (a Hyperdrive, a Durable Object, …), but for
// the kinds it deletes (KV, D1, R2, a Queue), which it says of itself, and the vars, secrets and assets that go with the
// Worker.
func TestNotDeleted(t *testing.T) {
	r := repo{wc: wconfig.Config{Bindings: map[string]string{"KV": "kv_namespace", "DB": "d1", "JOBS": "queue", "PG": "hyperdrive",
		"ROOMS": "durable_object_namespace", "MODE": "plain_text", "LIMITS": "json", "KEY": "secret_text", "ASSETS": "assets"}}}
	if got, want := r.notDeleted(nil), []string{"PG (hyperdrive)", "ROOMS (durable_object_namespace)"}; !slices.Equal(got, want) {
		t.Errorf("notDeleted: %q, want %q", got, want)
	}
	// A binding to the Worker's own Durable Object goes with it: delete says so of its own.
	if got, want := r.notDeleted(map[string]bool{"ROOMS": true}), []string{"PG (hyperdrive)"}; !slices.Equal(got, want) {
		t.Errorf("notDeleted, ROOMS the Worker's own: %q, want %q", got, want)
	}
	if got := (repo{wc: wconfig.Config{Bindings: map[string]string{"KV": "kv_namespace", "MODE": "plain_text"}}}).notDeleted(nil); got != nil {
		t.Errorf("the template's kinds only: %q, want none", got)
	}
}

// TestInit: init writes the template's tool half into a repo (toolfiles): each file as the template has it, the hook
// executable; to the repo's own .gitignore only the template's lines it lacks. Then nothing is stale. Its pin is the
// build that ran it; a release's init brings its parts of another tool's .mise/config.toml to what it writes (its pin
// moved forward, its own name beside the list's, use_versions_host), the rest left as it is, and then has nothing to
// write, or to report.
func TestInit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\n"), 0o644)
	initRepo(dir)
	own := map[string]bool{".mise/config.toml": true, ".gitignore": true, ".claude/settings.json": true}
	fs.WalkDir(templates.Tool, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || own[p] {
			return err
		}
		want, _ := fs.ReadFile(templates.Tool, p)
		if b, err := os.ReadFile(filepath.Join(dir, p)); err != nil || !bytes.Equal(b, want) {
			t.Errorf("%s isn't the template's: %v", p, err)
		}
		return nil
	})
	if st, err := os.Stat(filepath.Join(dir, ".claude", "hooks", templates.HookFile)); err != nil || runtime.GOOS != "windows" && st.Mode()&0o100 == 0 { // Windows has no executable bit
		t.Errorf("the hook isn't executable: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.HasPrefix(string(b), "dist/\n") || !strings.Contains(string(b), ".dev.vars") {
		t.Errorf("the repo's own .gitignore, with the template's lines: %q", b)
	}
	config := filepath.Join(dir, ".mise", "config.toml")
	if b, _ := os.ReadFile(config); !bytes.Equal(b, must2(configTool().Pinned())) || !bytes.Contains(b, []byte(`"`+self+`" = "local"`)) {
		t.Errorf(".mise/config.toml doesn't pin this build, local:\n%s", b)
	}
	if got := (repo{dir: dir}).stale(context.Background()); len(got) > 0 {
		t.Errorf("stale right after init: %q", got)
	}
	defer func(v string) { version = v }(version)
	version = "v0.1.36"
	os.WriteFile(config, []byte("# mise-check for this repo\n\n[settings]\nminimum_release_age_excludes = [\"npm:x\"]\n\n[tools]\n\""+self+"\" = \"0.1.7\"\n"), 0o644)
	initRepo(dir)
	b, _ := os.ReadFile(config)
	for _, part := range []string{"# mise-check for this repo\n", `["npm:x", "` + self + `"]`, "use_versions_host = false", `"` + self + `" = "0.1.36"`} {
		if !strings.Contains(string(b), part) {
			t.Errorf("init didn't keep %q in the repo's own .mise/config.toml:\n%s", part, b)
		}
	}
	if got := (repo{dir: dir}).stale(context.Background()); len(got) > 0 {
		t.Errorf("stale after init: %q", got)
	}
	if initRepo(dir); !bytes.Equal(b, must2(os.ReadFile(config))) {
		t.Errorf("init again changed .mise/config.toml")
	}
}

func must2(b []byte, _ error) []byte { return b }

// TestOlder: releases compare by number, part by part (0.1.7 is older than 0.1.36), never as text.
func TestOlder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.1.7", "0.1.36", true}, {"0.1.36", "0.1.7", false}, {"0.1.36", "0.1.36", false}, {"0.9.0", "1.0.0", true}, {"local", "0.1.36", false}} {
		if got := older(c.a, c.b); got != c.want {
			t.Errorf("older(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestNewestRelease: GitHub names the newest release, on its own page for it, as x.y.z.
func TestNewestRelease(t *testing.T) {
	if n := newestRelease(); !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(n) || older(n, "0.1.36") {
		t.Errorf("newestRelease: %q", n)
	}
}

// TestWithIDs: a resource takes its id from the binding of its name and kind that Cloudflare's API gives (a Flagship
// app's app_id, a KV's namespace_id), so delete needs no list; one named by name (a Queue, R2) or of another kind doesn't.
func TestWithIDs(t *testing.T) {
	rs := withIDs([]resource{{Binding: "FLAGS", Type: "flagship"}, {Binding: "KV", Type: "kv_namespace"}, {Binding: "JOBS", Type: "queue"}, {Binding: "DB", Type: "d1"}},
		map[string]map[string]any{"FLAGS": {"type": "flagship", "app_id": "a1"}, "KV": {"type": "kv_namespace", "namespace_id": "k1"},
			"JOBS": {"type": "queue", "queue_name": "w-jobs"}, "DB": {"type": "kv_namespace", "namespace_id": "x"}})
	var ids []string
	for _, p := range rs {
		ids = append(ids, p.ID)
	}
	if want := []string{"a1", "k1", "", ""}; !slices.Equal(ids, want) {
		t.Errorf("withIDs: %q, want %q", ids, want)
	}
}
