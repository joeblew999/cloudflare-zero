package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/joeblew999/mise-cloudflare/templates"
)

// workerConfigs are the files that make a folder a Worker's, whatever else it has: its wrangler config first.
var workerConfigs = []string{"wrangler.jsonc", "wrangler.json", "wrangler.toml", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb"}

// templateRepo writes the template's own Worker into a repo that has none: its code, its wrangler.jsonc and
// package.json under a name of the repo's own (its folder's), the lockfile, .dev.vars.example, and the mise.toml that
// installs its wrangler (node, aube, mise run setup). It never writes over a file. The Worker's files go in all
// together or not at all, so a repo with a Worker of its own (any of them, or a wrangler config or lockfile of
// another kind) gets none of the template's, and a mise.toml of the repo's own is left as it is, with what to add.
// README.md, the repo's front page, goes in whenever the repo has none, Worker or not. It reports whether it wrote
// the Worker.
func templateRepo(dir string) bool {
	name, err := workerName(filepath.Base(dir))
	if err != nil {
		fail(err)
	}
	var files []string
	must(fs.WalkDir(templates.Worker, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != "mise.toml" && p != "README.md" {
			files = append(files, p)
		}
		return err
	}))
	readme := filepath.Join(dir, "README.md")
	if _, err := os.Stat(readme); err == nil {
		fmt.Println("✓ README.md is the repo's own: left as it is")
	} else {
		b, _ := fs.ReadFile(templates.Worker, "README.md")
		writeFile(readme, bytes.ReplaceAll(b, []byte(templates.WorkerName), []byte(name)))
	}
	var own []string
	for _, f := range union(files, workerConfigs) {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			own = append(own, f)
		}
	}
	if len(own) > 0 {
		fmt.Printf("✓ the repo has a Worker of its own (%s): none of the template's written, its mise.toml included\n", strings.Join(own, ", "))
		return false
	}
	for _, f := range files {
		b, _ := fs.ReadFile(templates.Worker, f)
		if f == "wrangler.jsonc" || f == "package.json" {
			b = bytes.ReplaceAll(b, []byte(`"`+templates.WorkerName+`"`), []byte(`"`+name+`"`))
		}
		writeFile(filepath.Join(dir, filepath.FromSlash(f)), b)
	}
	tools, _ := fs.ReadFile(templates.Worker, "mise.toml")
	if _, err := os.Stat(filepath.Join(dir, "mise.toml")); err == nil {
		fmt.Printf("✓ mise.toml is the repo's own: left as it is. The template's Worker installs its wrangler with the template's:\n\n%s\n", indent(tools))
	} else {
		writeFile(filepath.Join(dir, "mise.toml"), tools)
	}
	fmt.Printf("✓ the template's Worker, %s\n", name)
	return true
}

// setUpWorker installs the Worker template wrote: mise trusts its mise.toml, then the repo's own setup task installs
// its wrangler (mise installs node and aube for it first). Without a setup task, it says how instead.
func setUpWorker(ctx context.Context, dir string) {
	trustConfig(dir, "mise.toml")
	has := exec.CommandContext(ctx, "mise", "tasks", "info", "setup")
	has.Dir = dir
	if has.Run() != nil {
		fmt.Println("the repo's mise.toml has no setup task: install the Worker's wrangler its own way (package.json pins it), then mise run cloudflare")
		return
	}
	fmt.Println("$ mise run setup")
	cmd := exec.CommandContext(ctx, "mise", "run", "setup")
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fail(fmt.Errorf("mise run setup: %w", err))
	}
	fmt.Println("✓ next: mise run cloudflare")
}

// trustConfig has mise trust a config file mise-cloudflare wrote into the repo, so its tasks run straight away: mise
// asks first about a config file it hasn't seen. Without mise on PATH, it says what to run.
func trustConfig(dir, file string) {
	path := filepath.Join(dir, filepath.FromSlash(file))
	if _, err := os.Stat(path); err != nil {
		return
	}
	if out, err := exec.Command("mise", "trust", "-q", path).CombinedOutput(); err != nil {
		fmt.Printf("mise didn't trust %s (%v %s): run mise trust %s\n", file, err, strings.TrimSpace(string(out)), file)
		return
	}
	fmt.Printf("✓ %s trusted by mise\n", file)
}

// workerName is a folder's name as a Worker's: lowercase letters, digits and dashes, at most 63 characters.
func workerName(folder string) (string, error) {
	name := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(folder), "-"), "-")
	if name == "" || len(name) > 63 {
		return "", fmt.Errorf("the folder %q makes no Worker name (lowercase letters, digits and dashes, at most 63): rename it", folder)
	}
	return name, nil
}

// union is a then b, each name once.
func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func indent(b []byte) string {
	return "    " + strings.ReplaceAll(strings.TrimRight(string(b), "\n"), "\n", "\n    ")
}

// hasWorker reports whether dir has a wrangler config: a Worker of its own.
func hasWorker(dir string) bool {
	for _, f := range workerConfigs[:3] {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// errNoWorker is what a command that needs the repo's Worker says in a repo that has none yet.
var errNoWorker = errors.New("no Worker here yet: bring the repo's own (a wrangler.jsonc, and wrangler in its package.json), or write the template's: mise run cloudflare:template")
