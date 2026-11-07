package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/joeblew999/mise-cloudflare/internal/cfapi"
	"github.com/joeblew999/mise-cloudflare/internal/wconfig"
	"github.com/joeblew999/mise-cloudflare/internal/wrangler"
)

// resource is one of a Worker's own KV namespaces, D1s, R2 buckets, Queues or Flagship apps, or one of its previews' own.
type resource struct {
	Binding string `json:"binding"`
	Type    string `json:"type"` // wrangler's: kv_namespace, d1, r2_bucket, queue or flagship
	Kind    string `json:"kind"` // KV, D1, R2, Queue or Flagship
	Name    string `json:"name"`
	ID      string `json:"id,omitempty"` // a KV namespace's, a D1's, a Queue's or a Flagship app's; an R2 bucket goes by its name
	Done    string `json:"done"`         // made, there already, deleted
	Gone    bool   `json:"gone"`         // off: deleted, or not there
}

// previewResources gives previews their own KV, D1, R2, Queue and Flagship app (<worker>-previews-<binding>), which
// Cloudflare doesn't, and puts them in the Worker's Previews Base. Nothing goes into the repo. --off deletes them. The Worker must be deployed first.
func (r repo) previewResources(ctx context.Context, off, jsonOut bool) []resource {
	out := r.previewNeeds()
	if len(out) == 0 {
		if !jsonOut {
			fmt.Printf("✓ %s has no KV, D1, R2, Queue or Flagship binding for its previews to get their own of\n", r.wc.Name)
		}
		return []resource{}
	}
	token, account := r.cloudflareAPI(ctx)
	if off {
		out = withIDs(out, r.previewsBase(ctx, token, account))
	}
	env := map[string]any{}
	for i, p := range out {
		var err error
		if off {
			env[p.Binding] = nil // a JSON merge patch's null takes the key out
			continue
		}
		if out[i].ID, out[i].Done, err = r.makeResource(ctx, p, token, account); err != nil {
			fail(err)
		}
		// In a preview's bindings, as wrangler sends them: R2 and a Queue by name, the others by id.
		id := out[i].ID
		if byName[p.Type] {
			id = p.Name
		}
		env[p.Binding] = map[string]string{"type": p.Type, previewsBaseField[p.Type]: id}
	}
	// Off, a Worker already deleted has no Previews Base left to take them out of: its resources still go.
	if err := patchPreviewsBase(ctx, token, account, r.wc.Name, env); err != nil && !(off && errors.Is(err, errNotOnCloudflare)) {
		fail(err)
	}
	if off {
		for i, p := range out {
			out[i].Done, out[i].Gone = r.deleteResource(ctx, p, token, account)
		}
	}
	if !jsonOut {
		for _, p := range out {
			mark := "✓"
			if off && !p.Gone {
				mark = "✗"
			}
			fmt.Printf("%s %s's previews: %s %s is %s (%s)\n", mark, r.wc.Name, p.Kind, p.Binding, p.Name, p.Done)
		}
		if !off {
			fmt.Printf("  in its Previews Base on Cloudflare, which every new preview starts from\n")
		}
	}
	if off {
		var left []string
		for _, p := range out {
			if !p.Gone {
				left = append(left, p.Kind+" "+p.Binding+", "+p.Name)
			}
		}
		if len(left) > 0 {
			fail(fmt.Errorf("not all deleted: the previews' %s is still on Cloudflare (what Cloudflare said is above)", strings.Join(left, "; ")))
		}
	}
	return out
}

// previewsBaseField is the field a binding names its resource by in the Previews Base, as Cloudflare's API takes it;
// byName the kinds it names by name, not id.
var (
	previewsBaseField = map[string]string{"kv_namespace": "namespace_id", "d1": "database_id", "r2_bucket": "bucket_name", "queue": "queue_name", "flagship": "app_id"}
	byName            = map[string]bool{"r2_bucket": true, "queue": true}
)

// previewNeeds is the previews' own KV, D1, R2, Queues and Flagship apps the Worker needs: one for each of its bindings of those kinds
// (wconfig.PreviewsOwn, by wrangler's own types) that its previews block leaves out.
func (r repo) previewNeeds() []resource {
	var out []resource
	for _, n := range slices.Sorted(maps.Keys(r.wc.Bindings)) {
		typ := r.wc.Bindings[n]
		kind, own := wconfig.PreviewsOwn[typ]
		if _, declared := r.wc.PreviewBindings[n]; own && !declared {
			out = append(out, resource{Binding: n, Type: typ, Kind: kind, Name: previewName(r.wc.Name, n)})
		}
	}
	return out
}

// previewsMissing is what the Previews Base lacks: a KV, D1, R2, Queue or Flagship binding added since, and the Worker's
// required secrets. Nil if it can't tell.
func (r repo) previewsMissing(ctx context.Context, token, account string) (resources, secrets []string) {
	base := r.previewsBase(ctx, token, account)
	if base == nil {
		return nil, nil
	}
	for _, p := range r.previewNeeds() {
		if _, ok := base[p.Binding]; !ok {
			resources = append(resources, p.Kind+" "+p.Binding)
		}
	}
	for _, s := range r.wc.Secrets {
		if _, ok := base[s]; !ok {
			secrets = append(secrets, s)
		}
	}
	return resources, secrets
}

// previewsBase is the bindings of the Worker's Previews Base on Cloudflare, by name, as its API gives them; nil if it
// can't tell (the Worker isn't deployed).
func (r repo) previewsBase(ctx context.Context, token, account string) map[string]map[string]any {
	got, err := cfapi.Call(ctx, token, http.MethodGet, "/accounts/"+account+"/workers/workers/"+url.PathEscape(r.wc.Name), nil, "")
	var res struct {
		Base struct {
			Env map[string]map[string]any `json:"env"`
		} `json:"previews_base_config"`
	}
	if err != nil || !got.OK() || json.Unmarshal(got.Result, &res) != nil {
		return nil
	}
	if res.Base.Env == nil {
		return map[string]map[string]any{}
	}
	return res.Base.Env
}

// withIDs is resources with each one's id from bindings (by name, as Cloudflare's API gives them: the Worker's own, or
// its Previews Base's) where a binding of its kind has one: what delete deletes it by. A list can lag what was just
// made (Flagship's does, for minutes), so the one it would find it in otherwise may not have it yet.
func withIDs(rs []resource, bindings map[string]map[string]any) []resource {
	for i, p := range rs {
		b := bindings[p.Binding]
		if id, ok := b[previewsBaseField[p.Type]].(string); ok && id != "" && b["type"] == p.Type && !byName[p.Type] {
			rs[i].ID = id
		}
	}
	return rs
}

// previewName is the name of the previews' own resource for a binding: <worker>-previews-<binding>, lowercase, with
// dashes (R2 allows no other), as wrangler names production's <worker>-<binding>.
func previewName(worker, binding string) string { return worker + "-previews-" + slug(binding) }

// slug is s lowercase, with dashes for anything but letters and digits: what a resource's or a file's name allows.
func slug(s string) string {
	return strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// findResource finds a resource of a kind delete deletes by its name, KV and D1 in the repo's wrangler's lists, the
// others through Cloudflare's API: an R2 bucket by its own address (200 there, 404 not), a Queue or a Flagship app in
// the API's list. It says whether it's there, and its id (none for an R2 bucket: its name is its address).
func (r repo) findResource(ctx context.Context, p resource, token, account string) (bool, string, error) {
	switch p.Type {
	case "kv_namespace":
		var list []struct{ ID, Title string }
		if err := r.wranglerJSON(ctx, &list, "kv", "namespace", "list"); err != nil {
			return false, "", err
		}
		for _, n := range list {
			if n.Title == p.Name {
				return true, n.ID, nil
			}
		}
	case "d1":
		var list []struct{ UUID, Name string }
		if err := r.wranglerJSON(ctx, &list, "d1", "list", "--json"); err != nil {
			return false, "", err
		}
		for _, d := range list {
			if d.Name == p.Name {
				return true, d.UUID, nil
			}
		}
	case "queue":
		return inList(ctx, token, "/accounts/"+account+"/queues", "queue_name", "queue_id", p.Name)
	case "flagship":
		return inList(ctx, token, "/accounts/"+account+"/flagship/apps", "name", "id", p.Name)
	case "r2_bucket":
		got, err := cfapi.Call(ctx, token, http.MethodGet, "/accounts/"+account+"/r2/buckets/"+url.PathEscape(p.Name), nil, "")
		switch {
		case err != nil:
			return false, "", err
		case got.Status == http.StatusNotFound:
			return false, "", nil
		case !got.OK():
			return false, "", fmt.Errorf("R2 bucket %s: %s", p.Name, got.Said())
		}
		return true, "", nil
	}
	return false, "", nil
}

// makeResource finds or makes the previews' own KV namespace, D1, R2 bucket, Queue or Flagship app, and returns its id and
// what it did.
func (r repo) makeResource(ctx context.Context, p resource, token, account string) (string, string, error) {
	if there, id, err := r.findResource(ctx, p, token, account); err != nil || there {
		return id, "there already", err
	}
	create := map[string][]string{"kv_namespace": {"kv", "namespace", "create", p.Name}, "d1": {"d1", "create", p.Name}, "r2_bucket": {"r2", "bucket", "create", p.Name},
		"queue": {"queues", "create", p.Name}, "flagship": {"flagship", "apps", "create", p.Name, "--json"}}
	out, err := wrangler.Output(r.w.Cmd(ctx, create[p.Type]...))
	if err != nil {
		return "", "", fmt.Errorf("making the previews' %s %s: %w", p.Kind, p.Name, err)
	}
	// A Flagship app's id is what its create prints (--json): Cloudflare's list of them lags, for minutes.
	var made struct{ ID string }
	if i := regexp.MustCompile(`(?m)^\{`).FindIndex(out); i != nil && json.Unmarshal(out[i[0]:], &made) == nil && made.ID != "" {
		return made.ID, "made", nil
	}
	// Cloudflare's list can lag what was just made (Flagship's does): it looks again every few seconds, for up to a minute.
	for until := time.Now().Add(time.Minute); ; time.Sleep(5 * time.Second) {
		there, id, err := r.findResource(ctx, p, token, account)
		switch {
		case err != nil:
			return "", "", err
		case there:
			return id, "made", nil
		case time.Now().After(until):
			return "", "", fmt.Errorf("made the previews' %s %s, then couldn't find it in Cloudflare's list for a minute", p.Kind, p.Name)
		}
	}
}

// deleteResource deletes a resource of a kind delete deletes with Cloudflare's API, and says how it went, by the API's
// status: deleted (2xx), not there (404), still holding objects (409, an R2 bucket's), else Cloudflare's own words; and
// whether it's gone.
// One whose id it has already (withIDs) it deletes by that id, finding nothing first.
func (r repo) deleteResource(ctx context.Context, p resource, token, account string) (done string, gone bool) {
	id := p.ID
	if id == "" {
		there, found, err := r.findResource(ctx, p, token, account)
		switch {
		case err != nil:
			return "not deleted: " + err.Error(), false
		case !there:
			return "not there", true
		}
		id = found
	}
	path := map[string]string{
		"kv_namespace": "/storage/kv/namespaces/" + url.PathEscape(id),
		"d1":           "/d1/database/" + url.PathEscape(id),
		"r2_bucket":    "/r2/buckets/" + url.PathEscape(p.Name),
		"queue":        "/queues/" + url.PathEscape(id),
		"flagship":     "/flagship/apps/" + url.PathEscape(id),
	}[p.Type]
	got, err := cfapi.Call(ctx, token, http.MethodDelete, "/accounts/"+account+path, nil, "")
	switch {
	case err != nil:
		return "not deleted: " + err.Error(), false
	case got.OK():
		return "deleted", true
	case got.Status == http.StatusNotFound:
		return "not there", true
	case got.Status == http.StatusConflict:
		return "not deleted: it still holds objects; delete them, then this again", false
	}
	return "not deleted: " + got.Said(), false
}

// inList finds name in the list Cloudflare's API gives at path, by each item's nameField: whether it's there, and its
// idField.
func inList(ctx context.Context, token, path, nameField, idField, name string) (bool, string, error) {
	got, err := cfapi.Call(ctx, token, http.MethodGet, path, nil, "")
	var list []map[string]any
	switch {
	case err != nil:
		return false, "", err
	case !got.OK() || json.Unmarshal(got.Result, &list) != nil:
		return false, "", fmt.Errorf("%s: %s", path, got.Said())
	}
	for _, it := range list {
		if it[nameField] == name {
			id, _ := it[idField].(string)
			return true, id, nil
		}
	}
	return false, "", nil
}

// cloudflareAPI is the repo's login's OAuth token (wrangler renews it) and its account, for Cloudflare's API.
func (r repo) cloudflareAPI(ctx context.Context) (token, account string) {
	who := r.ensureLogin(ctx)
	if len(who.Accounts) != 1 {
		fail(fmt.Errorf("the login %s can use %d accounts; log it in again and pick one", r.w.Profile, len(who.Accounts)))
	}
	token, err := r.w.Token(ctx)
	if err != nil {
		fail(err)
	}
	return token, who.Accounts[0].ID
}

// errNotOnCloudflare is a Worker that isn't deployed.
var errNotOnCloudflare = errors.New("isn't on Cloudflare yet")

// patchPreviewsBase merges env into the bindings of the Worker's Previews Base, as wrangler does for its secrets (a JSON
// merge patch of the Worker, so the base's other bindings and secrets stay).
func patchPreviewsBase(ctx context.Context, token, account, worker string, env map[string]any) error {
	body, _ := json.Marshal(map[string]any{"previews_base_config": map[string]any{"env": env}})
	got, err := cfapi.Call(ctx, token, http.MethodPatch, "/accounts/"+account+"/workers/workers/"+url.PathEscape(worker), body, "application/merge-patch+json")
	switch {
	case err != nil:
		return err
	case got.OK():
		return nil
	case got.Status == http.StatusNotFound:
		return fmt.Errorf("%s %w: deploy it once first (mise run cloudflare:deploy), then this again", worker, errNotOnCloudflare)
	}
	return fmt.Errorf("Cloudflare refused %s's Previews Base: %s", worker, got.Said())
}

// deletes is the binding kinds, by wrangler's own types, whose resources delete deletes when wrangler deploy made them:
// the kinds previews get their own of. wrangler deploy makes a few kinds more; delete names a binding of one as left.
var deletes = wconfig.PreviewsOwn

// ownResources is the resources wrangler deploy made for the Worker of the kinds delete deletes: its bindings of those
// kinds that name no resource, which deploy makes as <worker>-<binding>. kept is the ones the config names (an id, a database_name, a
// bucket_name): the developer's, which nothing here deletes.
func (r repo) ownResources() (own []resource, kept []string) {
	for _, n := range slices.Sorted(maps.Keys(r.wc.Bindings)) {
		typ := r.wc.Bindings[n]
		kind, ok := deletes[typ]
		switch {
		case !ok:
		case r.wc.Names(n):
			kept = append(kept, kind+" "+n)
		default:
			own = append(own, resource{Binding: n, Type: typ, Kind: kind, Name: r.wc.Name + "-" + slug(n)})
		}
	}
	return own, kept
}
