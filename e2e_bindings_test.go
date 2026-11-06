// The extras: bindings of kinds beyond the template's KV, D1 and R2, which the walk adds to a new repo's wrangler
// config as a developer would (mise run new-repo:e2e-extras, E2E_EXTRAS=true), to show on Cloudflare that wrangler
// deploy makes each one, cloudflare:11-preview makes the previews' own and binds it, and cloudflare:13-delete deletes
// both, and that cloudflare:06-serve runs one wrangler runs on Cloudflare even locally, on the repo's login. The template itself stays as minimal as it is. A kind is one row.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/joeblew999/mise-cloudflare/internal/cfapi"
)

// extra is one binding the walk adds: what the developer writes at the config's top level ({worker} is the Worker's
// name), its binding, and where Cloudflare's API lists what wrangler deploy makes for it, <worker>-<suffix>, by which
// field of each item, with its id in which (no list: it makes nothing); remote when wrangler runs it on Cloudflare even
// locally (its dev table marks it remote), which needs the repo's login.
type extra struct {
	add, binding, list, field, id, suffix string
	remote                                bool
}

// Of the kinds wrangler deploy makes, these aren't here: Dispatch Namespaces, which this account can't make (403:
// Workers for Platforms, a purchase), and AI Search and Agent Memory namespaces, which wrangler requires the config to
// name: the developer's, which 13-delete leaves (and says so), so a walk would leave them on the account.
var extras = []extra{
	{add: `"queues": { "producers": [{ "binding": "JOBS" }] }`, binding: "JOBS", list: "/queues", field: "queue_name", id: "queue_id", suffix: "jobs"},
	{add: `"flagship": [{ "binding": "FLAGS" }]`, binding: "FLAGS", list: "/flagship/apps", field: "name", id: "id", suffix: "flags"},
	{add: `"ai": { "binding": "AI" }`, binding: "AI", remote: true}, // Workers AI: wrangler runs it on Cloudflare even locally
}

// made is how many of the extras wrangler deploy makes something for.
func made() int {
	n := 0
	for _, e := range extras {
		if e.list != "" {
			n++
		}
	}
	return n
}

// withExtras reports whether this walk adds the extras (mise run new-repo:e2e-extras).
func withExtras() bool { return walkExtras }

// remoteExtras reports whether this walk adds an extra wrangler runs on Cloudflare even locally.
func remoteExtras() bool {
	return withExtras() && slices.ContainsFunc(extras, func(e extra) bool { return e.remote })
}

// addExtras writes the extras into the repo's wrangler.jsonc, as a developer adds bindings at its top level.
func (r repo) addExtras() {
	path := filepath.Join(r.Dir, "wrangler.jsonc")
	b, err := os.ReadFile(path)
	end := bytes.LastIndexByte(b, '}')
	if err != nil || end < 0 {
		r.T.Fatalf("%s: %v", path, err)
	}
	out := bytes.TrimRight(b[:end], " \n")
	for _, e := range extras {
		out = append(out, ",\n  "+strings.ReplaceAll(e.add, "{worker}", stampedWorker)...)
	}
	if err := os.WriteFile(path, append(append(out, '\n'), b[end:]...), 0o644); err != nil {
		r.T.Fatal(err)
	}
	r.Pass("the developer adds %d binding(s) beyond the template's to wrangler.jsonc: the extras", len(extras))
}

// extraOn is the resource Cloudflare's API lists on the account for an extra and worker, <worker>-<suffix> (worker is
// the Worker's name, or <worker>-previews for its previews' own): the list's item, or nil.
func (r repo) extraOn(token, account string, e extra, worker string) map[string]any {
	got, err := cfapi.Call(context.Background(), token, http.MethodGet, "/accounts/"+account+e.list, nil, "")
	var items []map[string]any
	if err != nil || !got.OK() || json.Unmarshal(got.Result, &items) != nil {
		r.T.Fatalf("Cloudflare's %s: %v %s", e.list, err, got.Said())
	}
	for _, it := range items {
		if it[e.field] == worker+"-"+e.suffix {
			return it
		}
	}
	return nil
}

// extrasOn names the extras' resources Cloudflare's API lists on the account for worker.
func (r repo) extrasOn(token, account, worker string) []string {
	var on []string
	for _, e := range extras {
		if e.list != "" && r.extraOn(token, account, e, worker) != nil {
			on = append(on, e.list[1:]+" "+worker+"-"+e.suffix)
		}
	}
	return on
}

// extrasSettle is extrasOn once Cloudflare's lists say what the walk expects, all of the extras (all) or none, looking
// again every few seconds for up to a minute, as a person would: a list can lag what was just made or deleted
// (Flagship's does).
func (r repo) extrasSettle(token, account, worker string, all bool) []string {
	until := time.Now().Add(time.Minute)
	for {
		on := r.extrasOn(token, account, worker)
		if all && len(on) == made() || !all && len(on) == 0 || time.Now().After(until) {
			return on
		}
		time.Sleep(5 * time.Second)
	}
}

// previewBindsOwn names the extras whose binding in a preview's env isn't the previews' own resource
// (<worker>-previews-<suffix>, by its name or its id, as the Previews Base gives it).
func (r repo) previewBindsOwn(token, account string, env map[string]any) []string {
	var wrong []string
	for _, e := range extras {
		if e.list == "" {
			continue
		}
		it, b := r.extraOn(token, account, e, r.worker+"-previews"), env[e.binding]
		got, _ := json.Marshal(b)
		if it == nil || b == nil || !bytes.Contains(got, []byte(fmt.Sprint(`"`, it[e.field], `"`))) && !bytes.Contains(got, []byte(fmt.Sprint(`"`, it[e.id], `"`))) {
			wrong = append(wrong, fmt.Sprintf("%s: %s", e.binding, got))
		}
	}
	return wrong
}

// withEnv is the wrangler environment a walk deploys instead of the config's top level (mise run new-repo:e2e-env:
// staging), or "": the developer adds it to the new repo's wrangler config, and every step runs with
// CLOUDFLARE_ENV naming it, as the docs say. The login stays the repo's one, named after the top level's Worker.
func withEnv() string { return walkEnv }

// addEnv writes the environment env into the repo's wrangler.jsonc, as a developer adds one: the template's bindings
// and secret again under env.<env>, since wrangler inherits neither; its previews block it inherits. Then the walk's
// steps run in it (CLOUDFLARE_ENV).
func (r repo) addEnv(env string) {
	if withExtras() {
		r.T.Fatal("the extras are at the config's top level, which an environment doesn't inherit: walk them alone")
	}
	path := filepath.Join(r.Dir, "wrangler.jsonc")
	b, err := os.ReadFile(path)
	end := bytes.LastIndexByte(b, '}')
	if err != nil || end < 0 {
		r.T.Fatalf("%s: %v", path, err)
	}
	block := `"env": { "` + env + `": { "d1_databases": [{ "binding": "DB" }], "kv_namespaces": [{ "binding": "KV" }], "r2_buckets": [{ "binding": "BUCKET" }], "secrets": { "required": ["GREETING"] } } }`
	out := append(bytes.TrimRight(b[:end], " \n"), ",\n  "+block+"\n"...)
	if err := os.WriteFile(path, append(out, b[end:]...), 0o644); err != nil {
		r.T.Fatal(err)
	}
	r.T.Setenv("CLOUDFLARE_ENV", env)
	r.Pass("the developer adds env.%s to wrangler.jsonc, with its own D1, KV, R2 and secret, and every step runs in it (CLOUDFLARE_ENV=%s)", env, env)
}
