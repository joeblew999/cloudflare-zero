package wconfig

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// -repos runs TestRepos (mise run repos:check): on the repos named after it, else repos.txt's.
var repos = flag.Bool("repos", false, "read every wrangler config in third-party repos: those named after -args, else repos.txt's")

// read reads a wrangler config file in environment env with the template's own wrangler, in a folder of its own as a
// repo would be, under the name wrangler looks for (wrangler.jsonc, .json or .toml, as the file is).
func read(t *testing.T, config, env string) (Config, error) {
	t.Helper()
	modules, _ := filepath.Abs(filepath.Join("..", "..", "templates", "node_modules"))
	if _, err := os.Stat(filepath.Join(modules, "wrangler")); err != nil {
		t.Skip("the template's wrangler isn't installed: mise run template:setup")
	}
	dir := t.TempDir()
	b, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "index.js"), []byte("export default {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "wrangler"+filepath.Ext(config)), b, 0o644)
	link(t, modules, filepath.Join(dir, "node_modules"))
	return ReadEnv(context.Background(), dir, env)
}

// link makes at point to the folder target: a symlink, or on Windows without the right to make one (no Developer Mode,
// no admin), a directory junction, which anyone may make.
func link(t *testing.T, target, at string) {
	t.Helper()
	err := os.Symlink(target, at)
	if err != nil && runtime.GOOS == "windows" {
		var out []byte
		if out, err = exec.Command("cmd", "/c", "mklink", "/J", at, target).CombinedOutput(); err != nil {
			err = fmt.Errorf("%v: %s", err, out)
		}
	}
	if err != nil {
		t.Skipf("no link to the template's node_modules here: %v", err)
	}
}

// TestAdopted: a project mise-cloudflare didn't make, read as wrangler reads it: production in env.production under its
// own name (its login the top level's, the repo's one), its resources named by ID (the developer's), a Durable Object and a Workflow, and no warnings.
func TestAdopted(t *testing.T) {
	for _, c := range []struct {
		env, name string
		remote    []string
	}{{"", "adopted-dev", []string{"AI", "CACHE"}}, {"production", "adopted", []string{"AI"}}} {
		w, err := read(t, filepath.Join("testdata", "adopted.jsonc"), c.env)
		if err != nil {
			t.Fatal(err)
		}
		if w.Name != c.name || w.Login != "adopted-dev" || !w.HasEnv("production") || w.HasEnv("staging") || w.Previews || w.Warnings != "" {
			t.Errorf("env %q: name %q, login %q, envs %q, previews %v, warnings %q", c.env, w.Name, w.Login, w.Envs, w.Previews, w.Warnings)
		}
		want := map[string]string{"DB": "hyperdrive", "CACHE": "kv_namespace", "LIMITER": "durable_object_namespace", "INDEX": "workflow"}
		for n, typ := range want {
			if w.Bindings[n] != typ {
				t.Errorf("env %q: binding %s is %q, want %q (all: %v)", c.env, n, w.Bindings[n], typ, w.Bindings)
			}
		}
		if !w.Names("CACHE") || !w.Names("DB") || !slices.Equal(w.Secrets, []string{"API_KEY", "SIGNING_KEY"}) {
			t.Errorf("env %q: named %q, secrets %q", c.env, w.Named, w.Secrets)
		}
		// Workers AI has no local simulator; development's CACHE is marked remote, production's isn't.
		if !slices.Equal(w.Remote, c.remote) {
			t.Errorf("env %q: remote %q, want %q", c.env, w.Remote, c.remote)
		}
	}
}

// TestTemplate: the template's own config: its KV, D1 and R2 name nothing, so wrangler deploy makes them.
func TestTemplate(t *testing.T) {
	w, err := read(t, filepath.Join("..", "..", "templates", "wrangler.jsonc"), "")
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "mise-cloudflare-template" || len(w.Named) != 0 || !w.Previews || len(w.Envs) != 0 || len(w.Remote) != 0 || w.Warnings != "" {
		t.Errorf("name %q, named %q, previews %v, envs %q, remote %q, warnings %q", w.Name, w.Named, w.Previews, w.Envs, w.Remote, w.Warnings)
	}
	for n, typ := range map[string]string{"DB": "d1", "KV": "kv_namespace", "BUCKET": "r2_bucket"} {
		if w.Bindings[n] != typ {
			t.Errorf("binding %s is %q, want %q", n, w.Bindings[n], typ)
		}
	}
}

// TestBehindProxy: on a machine whose network goes through a proxy, the config still reads with no warnings: wrangler's
// notice that it will use the proxy is about the machine, not the config (issue #27).
func TestBehindProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	w, err := read(t, filepath.Join("..", "..", "templates", "wrangler.jsonc"), "")
	if err != nil {
		t.Fatal(err)
	}
	if w.Warnings != "" {
		t.Errorf("behind a proxy, warnings %q, want none", w.Warnings)
	}
}

// TestBroken: wrangler's own validation comes through: an unexpected field as its warning, a wrong type as its error.
func TestBroken(t *testing.T) {
	w, err := read(t, filepath.Join("testdata", "broken-field.jsonc"), "")
	if err != nil || !strings.Contains(w.Warnings, "kv_namespacez") {
		t.Errorf("an unexpected field: warnings %q, err %v", w.Warnings, err)
	}
	if _, err := read(t, filepath.Join("testdata", "broken-type.jsonc"), ""); err == nil || !strings.Contains(err.Error(), "wrangler can't read the config") || !strings.Contains(err.Error(), "minify") {
		t.Errorf("a wrong type: err %v", err)
	}
	w, err = read(t, filepath.Join("testdata", "bare.jsonc"), "")
	if err != nil || w.Previews || len(w.Bindings) != 0 || w.Warnings != "" {
		t.Errorf("bare: %+v, %v", w, err)
	}
}

// TestRepos reads every wrangler config in third-party repos with our wrangler, at the top level and in each
// environment, as the tasks would: the repos named (owner/repo, or owner/repo@commit), else repos.txt's, each cloned
// into repos/<owner>/<repo>. A config the bridge can't read fails; one wrangler warns about, or that asks for a later
// compatibility date than our wrangler's runtime has, is reported.
func TestRepos(t *testing.T) {
	if !*repos {
		t.Skip("mise run repos:check runs it: it clones")
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	// The tool itself, built from here, for its own check in each config's folder: into a folder, so go names it for
	// the OS (.exe on Windows), and exec finds it by its name alone there too.
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "mise-cloudflare")
	if out, err := exec.Command("go", "build", "-o", tmp+string(filepath.Separator), filepath.Join(root, "cmd", "mise-cloudflare")).CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	list := flag.Args()
	if len(list) == 0 {
		b, err := os.ReadFile(filepath.Join(root, "repos.txt"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				list = append(list, line)
			}
		}
	}
	for _, repo := range list {
		name, ref, _ := strings.Cut(repo, "@")
		dir := filepath.Join(root, "repos", filepath.FromSlash(name))
		if _, err := os.Stat(dir); err != nil {
			git(t, "", "init", "-q", dir)
			git(t, dir, "remote", "add", "origin", "https://github.com/"+name+".git")
		}
		git(t, dir, "fetch", "-q", "--depth", "1", "origin", cmpOr(ref, "HEAD"))
		git(t, dir, "checkout", "-q", "--detach", "FETCH_HEAD")
		git(t, dir, "clean", "-qfdx")
		t.Logf("%s at %s", name, git(t, dir, "rev-parse", "--short", "HEAD"))
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && slices.Contains([]string{"node_modules", ".git", ".wrangler"}, d.Name()) {
				return filepath.SkipDir
			}
			if !d.IsDir() && slices.Contains([]string{"wrangler.json", "wrangler.jsonc", "wrangler.toml"}, d.Name()) {
				rel, _ := filepath.Rel(filepath.Join(root, "repos"), path)
				t.Run(filepath.ToSlash(rel), func(t *testing.T) {
					t.Parallel()
					report(t, path, dir, bin)
				})
			}
			return nil
		})
	}
}

// linked puts the template's wrangler beside a config where it is, as a repo's own install would (the files it names, a
// Dockerfile, its main, are then where it says), until the test ends.
func linked(t *testing.T, dir string) {
	t.Helper()
	modules, _ := filepath.Abs(filepath.Join("..", "..", "templates", "node_modules"))
	at := filepath.Join(dir, "node_modules")
	if _, err := os.Lstat(at); err == nil {
		return
	}
	link(t, modules, at)
	t.Cleanup(func() { os.Remove(at) })
}

// report reads one config of the repo cloned in clone where it is, at its top level and in each environment, one line
// each; then runs the tool's own check there (bin check --json), as a developer would, and lists what it finds.
func report(t *testing.T, path, clone, bin string) {
	dir := filepath.Dir(path)
	linked(t, dir)
	w, err := ReadEnv(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("FAIL %v", err)
	}
	for _, env := range append([]string{""}, w.Envs...) {
		if env != "" {
			if w, err = ReadEnv(context.Background(), dir, env); err != nil {
				t.Errorf("FAIL env.%s: %v", env, err)
				continue
			}
		}
		verdict, date, name := "PASS", w.CompatibilityDate, w.Name
		switch {
		case date == "":
			verdict, date = "WARN", "none"
		case date > w.DevDate:
			verdict, date = "WARN", date+" (later than our wrangler dev's default, "+w.DevDate+", its runtime's)"
		}
		if name == "" {
			verdict, name = "WARN", "none (wrangler deploy needs one)"
		}
		if w.Warnings != "" {
			verdict = "WARN"
		}
		var bindings []string
		for _, n := range slices.Sorted(maps.Keys(w.Bindings)) {
			b := n + ":" + w.Bindings[n]
			if _, own := PreviewsOwn[w.Bindings[n]]; own && w.Names(n) {
				b += "(named)"
			}
			if slices.Contains(w.Remote, n) {
				b += "(remote)"
			}
			bindings = append(bindings, b)
		}
		t.Logf("%s %s name=%s date=%s wrangler=%s bindings=[%s] secrets=%d previews=%v",
			verdict, cmpOr(env, "top"), name, date, pinned(path, clone), strings.Join(bindings, " "), len(w.Secrets), w.Previews)
		if w.Warnings != "" {
			t.Logf("     wrangler warns: %s", strings.ReplaceAll(w.Warnings, "\n", " / "))
		}
	}
	cmd := exec.Command(bin, "check", "--json")
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	var c struct {
		OK       bool     `json:"ok"`
		Problems []string `json:"problems"`
	}
	if json.Unmarshal(out, &c) != nil {
		t.Logf("     check: %s", strings.TrimSpace(stderr.String()))
	}
	for _, p := range c.Problems {
		t.Logf("     check: %s", strings.ReplaceAll(p, "\n", " / "))
	}
}

// pinned is the wrangler version the nearest package.json above path, in the repo cloned in clone, pins, or "-".
func pinned(path, clone string) string {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		var p struct{ Dependencies, DevDependencies map[string]string }
		if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil && json.Unmarshal(b, &p) == nil {
			if v := cmpOr(p.DevDependencies["wrangler"], p.Dependencies["wrangler"]); v != "" {
				return v
			}
		}
		if dir == clone || dir == filepath.Dir(dir) {
			return "-"
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	// Long paths: a repo's deep folders, under repos/ here, can pass Windows' 260 characters.
	cmd := exec.Command("git", append([]string{"-c", "core.longpaths=true"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func cmpOr(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
