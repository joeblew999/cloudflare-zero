// Package templates is the template: a whole repo with mise-cloudflare, which runs wherever it is put. It has
// two halves. The tool's half, listed below, is what mise-cloudflare init writes into any repo, at the same paths: the
// .mise folder (the tasks, and mise-cloudflare's pin, which mise install gets from its GitHub releases), the GitHub
// workflow, the Claude Code hook and its settings, the agent skill, the docs and the .gitignore. The rest is the
// template's own Worker (src/, wrangler.jsonc, package.json and its lockfile, mise.toml with what installs its
// wrangler, .dev.vars.example, README.md), there so the template runs: init never writes it, and mise-cloudflare
// template writes it into a repo that has no Worker yet. mise-check's files (its tasks, workflow, skill, hook and
// docs, and its parts of .mise/config.toml, .gitignore and .claude/settings.json) are its own init's, which
// mise-cloudflare init runs: they're here as that init writes them, so the template is a repo after init. What is for
// this repo itself (its own mise.toml, tests, CI and README) is never here, and neither is the machine's own
// .mise/config.local.toml.
package templates

import (
	"embed"
	"io/fs"
)

//go:embed .mise/tasks/mise-cloudflare.toml .mise/config.toml
//go:embed .github/workflows/cloudflare.yml
//go:embed .claude/hooks/mise-cloudflare-session-start.sh .claude/settings.json .claude/skills/cloudflare
//go:embed docs/mise-cloudflare
//go:embed .gitignore
var tool embed.FS

// Tool is the template's tool half: the files init writes into a repo, at their paths there.
var Tool fs.FS = tool

//go:embed src wrangler.jsonc package.json pnpm-lock.yaml .dev.vars.example mise.toml README.md
var worker embed.FS

// Worker is the template's own Worker: the files mise-cloudflare template writes into a repo that has none, at their
// paths there. Its mise.toml installs its wrangler (node, aube, mise run setup), and README.md is the repo's front
// page.
var Worker fs.FS = worker

// WorkerName is the template's Worker's name, in its wrangler.jsonc and package.json: a repo gets its own instead.
const WorkerName = "mise-cloudflare-template"

// Tasks is the repo's mise tasks, .mise/tasks/mise-cloudflare.toml.
var Tasks, _ = fs.ReadFile(Tool, ".mise/tasks/mise-cloudflare.toml")

// HookFile is the Claude Code hook's name in a repo's .claude/hooks.
const HookFile = "mise-cloudflare-session-start.sh"
