// Package wconfig asks the repo's own wrangler for its config, so nothing here parses one, and nothing outside it sees
// the file as written: what each environment deploys is wrangler's answer, at the repo's own wrangler version.
package wconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// Config is what the tasks need from a repo's wrangler config, as wrangler reads it.
type Config struct {
	Path    string   `json:"path"`    // the file wrangler read
	Name    string   `json:"name"`    // the Worker's name, as wrangler resolves it (CLOUDFLARE_ENV included)
	Login   string   `json:"login"`   // the top level's name, whichever environment: the repo's one login is named after it
	Secrets []string `json:"secrets"` // secrets.required
	Envs    []string `json:"envs"`    // the environments the file defines (env.<name>)
	// The Worker's bindings and its previews block's, name → type, as wrangler itself converts them (kv_namespace, d1,
	// queue, workflow, …).
	Bindings        map[string]string `json:"bindings"`
	PreviewBindings map[string]string `json:"previewBindings"`
	// Named and PreviewNamed are the bindings that name the resource they use (wrangler's converted binding has more than
	// its type: an id, a database_name, a bucket_name, …): the developer's, never made or deleted here. One that names
	// none is made by wrangler deploy, by the Worker's name.
	Named        []string `json:"named"`
	PreviewNamed []string `json:"previewNamed"`
	Previews     bool     `json:"previews"` // it has a previews block
	// Remote is the bindings wrangler dev runs on Cloudflare even locally (one with no local simulator, as Workers AI,
	// or marked "remote": true), as wrangler itself picks them: they need the repo's login, and their resource there.
	Remote []string `json:"remote"`
	// CompatibilityDate is the config's compatibility_date; DevDate the one this wrangler's dev uses when a config gives
	// none (its runtime's own date in recent wranglers, today's in older ones).
	CompatibilityDate string `json:"compatibilityDate"`
	DevDate           string `json:"devDate"`
	Warnings          string `json:"-"` // what wrangler said about the config reading it, its own validation
}

// PreviewsOwn is the binding kinds, by wrangler's own types, whose resources previews get of their own from mise-cloudflare
// (wrangler deploy makes them by name for production, wrangler preview makes none): their labels.
var PreviewsOwn = map[string]string{"kv_namespace": "KV", "d1": "D1", "r2_bucket": "R2", "queue": "Queue", "flagship": "Flagship"}

// script reads the config with the repo's own wrangler and prints it as one marked line, or wrangler's own error.
const script = `const w = await import('wrangler');
try {
  const c = w.unstable_readConfig({ env: process.env.CLOUDFLARE_ENV || undefined });
  const r = await w.experimental_readRawConfig({});
  // Older wranglers (4.49) can't convert a block that isn't there, and list a binding with no name (assets without
  // one) under "undefined": neither is a binding the Worker sees.
  const conv = (b) => Object.fromEntries(Object.entries(b ? w.unstable_convertConfigBindingsToStartWorkerBindings(b) ?? {} : {})
    .filter(([n]) => n !== 'undefined'));
  const types = (b) => Object.fromEntries(Object.entries(conv(b)).map(([n, v]) => [n, v.type]));
  // A binding names its resource when wrangler's converted binding has a value besides its type: the fields it was
  // given no value for are there too, undefined.
  const given = (v) => Object.entries(v).some(([k, x]) => k !== 'type' && x !== undefined && x !== null && x !== '');
  const named = (b) => Object.entries(conv(b)).filter(([, v]) => given(v)).map(([n]) => n);
  // wrangler's own pick of the bindings dev runs remotely: asked to update a session of ours, its remote proxy session
  // call hands that session those bindings and starts nothing.
  const session = { ready: Promise.resolve(), updateBindings: async () => {}, dispose: async () => {} };
  const picked = await w.maybeStartOrUpdateRemoteProxySession({ name: c.name ?? 'worker', bindings: conv(c) }, { session, remoteBindings: {} });
  console.log('WCONFIG ' + JSON.stringify({ path: c.configPath, name: c.name, secrets: c.secrets?.required ?? [],
    login: r.rawConfig.name ?? c.name, envs: Object.keys(r.rawConfig.env ?? {}), previews: c.previews !== undefined,
    compatibilityDate: c.compatibility_date ?? '', devDate: w.unstable_getDevCompatibilityDate?.() ?? '',
    bindings: types(c), previewBindings: types(c.previews), named: named(c), previewNamed: named(c.previews),
    remote: Object.keys(picked?.remoteBindings ?? {}).sort() }));
} catch (e) {
  console.log('WCONFIG_ERROR ' + JSON.stringify(String(e?.message ?? e)));
}`

// Read asks the wrangler installed in dir (node_modules) for dir's config, in CLOUDFLARE_ENV's environment if it's set.
func Read(ctx context.Context, dir string) (Config, error) {
	return ReadEnv(ctx, dir, os.Getenv("CLOUDFLARE_ENV"))
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// ReadEnv is Read in the wrangler environment env ("" for none): the Worker that environment deploys.
func ReadEnv(ctx context.Context, dir, env string) (Config, error) {
	var c Config
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", script)
	cmd.Dir = dir
	cmd.Env = append(withoutProxy(os.Environ()), "CLOUDFLARE_ENV="+env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return c, fmt.Errorf("wrangler couldn't run here (install the repo's dependencies first: its package.json pins wrangler): %w\n%s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	// wrangler prints what it finds wrong in the config (unexpected fields, …) around the marked line.
	var said []string
	found := false
	for _, line := range strings.Split(ansi.ReplaceAllString(string(out)+"\n"+stderr.String(), ""), "\n") {
		if b, ok := strings.CutPrefix(line, "WCONFIG_ERROR "); ok {
			var msg string
			json.Unmarshal([]byte(b), &msg)
			return c, errors.New("wrangler can't read the config: " + strings.TrimSpace(msg))
		}
		if b, ok := strings.CutPrefix(line, "WCONFIG "); ok {
			if err := json.Unmarshal([]byte(b), &c); err != nil {
				return c, err
			}
			found = true
			continue
		}
		if strings.TrimSpace(line) != "" {
			said = append(said, strings.TrimRight(line, " "))
		}
	}
	if !found {
		return c, fmt.Errorf("wrangler printed no config here:\n%s", strings.Join(said, "\n"))
	}
	c.Warnings = strings.Join(said, "\n")
	return c, nil
}

// withoutProxy is env without the proxy settings. Reading a config uses no network, and wrangler, given a proxy,
// says it will use it: a notice about the machine, not the config, which would read as a problem with it.
func withoutProxy(env []string) []string {
	var out []string
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(name) {
		case "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, e)
	}
	return out
}

// HasEnv reports whether the config defines the wrangler environment env (env.<env>).
func (c Config) HasEnv(env string) bool { return slices.Contains(c.Envs, env) }

// Names reports whether the binding names the resource it uses: the developer's, so never made or deleted here.
func (c Config) Names(binding string) bool { return slices.Contains(c.Named, binding) }
