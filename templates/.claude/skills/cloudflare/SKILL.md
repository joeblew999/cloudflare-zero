---
name: cloudflare
description: Use when working on this repo's Cloudflare Worker (run it, deploy it, previews, staging, production, D1, KV, R2, secrets, logs, CI) or its Cloudflare login. The repo uses mise-cloudflare, at the version its .mise/config.toml pins; every command is a task, mise run cloudflare:<command>. Never create or ask for a Cloudflare API token.
---

# This repo's Cloudflare Worker

This repo runs its Worker with mise-cloudflare, at the version its `.mise/config.toml` pins. Everything about it, every
command, the logins, previews, staging and production, and what to do when something is refused, is in this repo's
[docs/mise-cloudflare/README.md](../../../docs/mise-cloudflare/README.md): read it first.

Start with `mise run cloudflare`: it shows the steps, which are done, and what to run next.

The tasks are numbered in the order a repo meets them, and each runs the command of its name
(`mise run cloudflare:08-deploy` runs `mise-cloudflare deploy`): `03-init`, `04-check`, `05-show`, `06-serve`,
`07-set-secrets`, `08-deploy`, `09-connect-github`, `10-set-preview-secrets`, `11-preview`, `12-login`, `13-delete`,
`14-logout`, `15-clean`, each `mise run cloudflare:<it>`. Steps 00 to 02 come before them: the repo's git, `mise install`, its own
`setup`. And `mise run cloudflare:verify` (what GitHub runs), `mise run cloudflare:run-wrangler <...>` (any other
wrangler command on the repo's login: `d1 list`, `secret put`, `tail`, …), `mise run cloudflare:template` (the
template's Worker, in a repo with none yet), `mise run cloudflare:update` (the repo's own
pins at their newest releases, by mise-check: mise's tools, packages, Go and Go modules), `mise run cloudflare:show-help`.

What GitHub runs on every push is `.github/workflows/cloudflare.yml`, which checks out, installs mise, then runs
`mise run cloudflare:verify`. Run that task here and you see what GitHub sees.

## Never

- Never run `npx wrangler`, `wrangler login` or a global wrangler: they use another login than the repo's. Run
  wrangler through `mise run cloudflare:run-wrangler`.
- Never create, ask for or store a Cloudflare API token.
- Never click Allow for the person. When a task prints a login link (in a Claude Code cloud session it has the code
  already in it), give them that link as the first line of a message, so they open it on any device, click Allow and
  pick the account.
- Never edit what `mise run cloudflare:03-init` writes: `.mise/tasks/mise-cloudflare.toml`, `docs/mise-cloudflare/`,
  this skill, `.github/workflows/cloudflare.yml` and the Claude Code hook. After upgrading mise-cloudflare, run
  `mise run cloudflare:03-init`, which writes them again.
- Never weaken a rule `mise run cloudflare:04-check` reports: fix the repo.
