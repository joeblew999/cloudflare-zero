---
name: cloudflare
description: Use when working on this repo's Cloudflare Worker: run it locally, deploy it, previews, staging, production, its D1, KV, R2, Queues and secrets, logs, its GitHub CI, or its Cloudflare login. The repo uses mise-cloudflare, at the version its .mise/config.toml pins, and every command is a task, mise run cloudflare:<command>. Never create or ask for a Cloudflare API token, never run wrangler any other way, never click Allow for the person.
---

# This repo's Cloudflare Worker

This repo runs its Worker with mise-cloudflare, at the version its `.mise/config.toml` pins. Everything about it, the
logins, previews, staging and production, and what to do when something is refused, is in this repo's
[docs/mise-cloudflare/README.md](../../../docs/mise-cloudflare/README.md): read it when a task's output isn't enough.

Start with `mise run cloudflare`: it shows the steps, which are done, and what to run next. Follow what it says.

## The steps, in the order a repo takes them

Before them: the repo's git, `mise install`, and its own `mise run setup`. Each task runs the mise-cloudflare command
of its name (`mise run cloudflare:deploy` runs `mise-cloudflare deploy`).

| Task | Run it | What it does |
|---|---|---|
| `mise run cloudflare:init` | after upgrading mise-cloudflare | writes mise-cloudflare's files again: its tasks, the workflow, this skill, the docs, the hook |
| `mise run cloudflare:check` | after any change, before deploying | the repo's rules: no tokens or account IDs in it, secrets git-ignored, init's files current |
| `mise run cloudflare:show` | to see where the Worker is | what's done, and what to run next (the same as `mise run cloudflare`) |
| `mise run cloudflare:serve` | to run the Worker locally | `wrangler dev` with local D1, KV and R2: no account needed (a binding wrangler runs on Cloudflare even here logs the repo in) |
| `mise run cloudflare:set-secrets` | before the first deploy, and for a new secret | asks for each secret the Worker requires that Cloudflare doesn't have yet |
| `mise run cloudflare:deploy` | to put the Worker on Cloudflare | `wrangler deploy` on the repo's own login; the first time, the person clicks Allow |
| `mise run cloudflare:connect-github` | once, so Cloudflare deploys every push | Workers Builds: a preview for every pull request, production from the default branch |
| `mise run cloudflare:set-preview-secrets` | before the first preview (`cloudflare:preview`, or a pull request's after connect-github) | asks for each secret previews need, in the Previews Base: previews don't inherit the Worker's |
| `mise run cloudflare:preview` | to try what's here at its own URL | `wrangler preview`, with its own KV, D1, R2, Queues and Flagship apps |
| `mise run cloudflare:login` | only when asked to log in first | the repo's Cloudflare login; the other tasks make it when they need it |
| `mise run cloudflare:delete` | to undo deploy | the Worker deleted, then its own KV, D1, R2, Queues, Flagship apps and its previews' own; it asks first |
| `mise run cloudflare:logout` | to end the repo's logins | wrangler's and cf's logins for the repo, logged out and deleted |
| `mise run cloudflare:clean` | to start local runs from nothing | `.wrangler/` (local D1, KV, R2) and `.dev.vars` deleted; it asks first |

## The other tasks

| Task | Run it | What it does |
|---|---|---|
| `mise run cloudflare` | first, and whenever you're unsure | what's done for this Worker, and what to run next |
| `mise run cloudflare:verify` | before every push | what `.github/workflows/cloudflare.yml` runs on GitHub: the repo's `setup` and `test`, then check |
| `mise run cloudflare:run-wrangler` | for any other wrangler command | wrangler on the repo's login, the command passed on as it is: `d1 list`, `secret put NAME`, `tail` |
| `mise run cloudflare:template` | only in a repo with no Worker yet | the template's Worker, named for the repo's folder; it never writes over a file |
| `mise run cloudflare:show-version` | to say which mise-cloudflare this is | its version |
| `mise run cloudflare:show-help` | to list every command | every command |

## Flags and arguments

- `--json`: one JSON document on stdout instead of text, on `mise run cloudflare`, `cloudflare:check`, `cloudflare:show`,
  `cloudflare:connect-github` and `cloudflare:login`. Read that, never the text.
- `--yes`: on `cloudflare:delete` and `cloudflare:clean`, don't ask first. Pass it only when the person has said to delete.
- `--off`: on `cloudflare:connect-github`, turn Workers Builds off: its setup and the previews' resources deleted.
- `--branch <branch>`: on `cloudflare:connect-github`, the branch whose pushes deploy production (default the repository's
  default branch, or `production` with `--staging`).
- `--staging <branch>`: on `cloudflare:connect-github`, a branch whose pushes deploy the wrangler environment `env.staging`
  (`--staging main`).
- `--build <command>`: on `cloudflare:connect-github`, the build command (default installing from the lockfile).
- Arguments after `cloudflare:serve`, `cloudflare:deploy`, `cloudflare:preview` and `cloudflare:run-wrangler` go to wrangler as they are:
  `mise run cloudflare:serve --port 8787`, `mise run cloudflare:preview --name my-test`.

A deploy, a preview or a login needs the person once, to click Allow. With `CLOUDFLARE_LOGIN=device`, or in a Claude
Code cloud session, the task prints a link with the code in it: give the person that link as the first line of a
message, so they open it on any device. CI never deploys: Workers Builds does, after `cloudflare:connect-github`.

## Never

- Never run `npx wrangler`, `wrangler login` or a global wrangler: they use another login than the repo's. Run
  wrangler through `mise run cloudflare:run-wrangler`.
- Never create, ask for or store a Cloudflare API token.
- Never click Allow for the person, and never pass `--yes` without their say.
- Never upgrade a pin by hand: `mise run mise-check:update` (mise-check's own task, which init gives the repo).
- Never edit what `mise run cloudflare:init` writes: `.mise/tasks/mise-cloudflare.toml`, `docs/mise-cloudflare/`,
  this skill, `.github/workflows/cloudflare.yml` and the Claude Code hook. After upgrading mise-cloudflare, run
  `mise run cloudflare:init`, which writes them again.
- Never weaken a rule `mise run cloudflare:check` reports: fix the repo.
