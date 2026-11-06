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
of its name (`mise run cloudflare:08-deploy` runs `mise-cloudflare deploy`).

| Task | Run it | What it does |
|---|---|---|
| `mise run cloudflare:03-init` | after upgrading mise-cloudflare | writes mise-cloudflare's files again: its tasks, the workflow, this skill, the docs, the hook |
| `mise run cloudflare:04-check` | after any change, before deploying | the repo's rules: no tokens or account IDs in it, secrets git-ignored, init's files current |
| `mise run cloudflare:05-show` | to see where the Worker is | what's done, and what to run next (the same as `mise run cloudflare`) |
| `mise run cloudflare:06-serve` | to run the Worker locally | `wrangler dev` with local D1, KV and R2: no account needed (a binding wrangler runs on Cloudflare even here logs the repo in) |
| `mise run cloudflare:07-set-secrets` | before the first deploy, and for a new secret | asks for each secret the Worker requires that Cloudflare doesn't have yet |
| `mise run cloudflare:08-deploy` | to put the Worker on Cloudflare | `wrangler deploy` on the repo's own login; the first time, the person clicks Allow |
| `mise run cloudflare:09-connect-github` | once, so Cloudflare deploys every push | Workers Builds: a preview for every pull request, production from the default branch |
| `mise run cloudflare:10-set-preview-secrets` | before the first preview (`11-preview`, or a pull request's after connect-github) | asks for each secret previews need, in the Previews Base: previews don't inherit the Worker's |
| `mise run cloudflare:11-preview` | to try what's here at its own URL | `wrangler preview`, with its own KV, D1, R2, Queues and Flagship apps |
| `mise run cloudflare:12-login` | only when asked to log in first | the repo's Cloudflare login; the other tasks make it when they need it |
| `mise run cloudflare:13-delete` | to undo deploy | the Worker deleted, then its own KV, D1, R2, Queues, Flagship apps and its previews' own; it asks first |
| `mise run cloudflare:14-logout` | to end the repo's logins | wrangler's and cf's logins for the repo, logged out and deleted |
| `mise run cloudflare:15-clean` | to start local runs from nothing | `.wrangler/` (local D1, KV, R2) and `.dev.vars` deleted; it asks first |

## The other tasks

| Task | Run it | What it does |
|---|---|---|
| `mise run cloudflare` | first, and whenever you're unsure | what's done for this Worker, and what to run next |
| `mise run cloudflare:verify` | before every push | what `.github/workflows/cloudflare.yml` runs on GitHub: the repo's `setup` and `test`, then check |
| `mise run cloudflare:run-wrangler` | for any other wrangler command | wrangler on the repo's login, the command passed on as it is: `d1 list`, `secret put NAME`, `tail` |
| `mise run cloudflare:update` | when the person asks to upgrade | every pin at its newest, by mise-check: mise's tools, the packages (aube), Go and Go modules |
| `mise run cloudflare:template` | only in a repo with no Worker yet | the template's Worker, named for the repo's folder; it never writes over a file |
| `mise run cloudflare:show-version` | to say which mise-cloudflare this is | its version |
| `mise run cloudflare:show-help` | to list every command | every command |

## Flags and arguments

- `--json`: one JSON document on stdout instead of text, on `mise run cloudflare`, `04-check`, `05-show`,
  `09-connect-github` and `12-login`, and on `update` every pin before and after. Read that, never the text.
- `--yes`: on `13-delete` and `15-clean`, don't ask first. Pass it only when the person has said to delete.
- `--off`: on `09-connect-github`, turn Workers Builds off: its setup and the previews' resources deleted.
- `--branch <branch>`: on `09-connect-github`, the branch whose pushes deploy production (default the repository's
  default branch, or `production` with `--staging`).
- `--staging <branch>`: on `09-connect-github`, a branch whose pushes deploy the wrangler environment `env.staging`
  (`--staging main`).
- `--build <command>`: on `09-connect-github`, the build command (default installing from the lockfile).
- `--no-major`: on `update`, no major moves.
- Arguments after `06-serve`, `08-deploy`, `11-preview` and `run-wrangler` go to wrangler as they are:
  `mise run cloudflare:06-serve --port 8787`, `mise run cloudflare:11-preview --name my-test`.

A deploy, a preview or a login needs the person once, to click Allow. With `CLOUDFLARE_LOGIN=device`, or in a Claude
Code cloud session, the task prints a link with the code in it: give the person that link as the first line of a
message, so they open it on any device. CI never deploys: Workers Builds does, after `09-connect-github`.

## Never

- Never run `npx wrangler`, `wrangler login` or a global wrangler: they use another login than the repo's. Run
  wrangler through `mise run cloudflare:run-wrangler`.
- Never create, ask for or store a Cloudflare API token.
- Never click Allow for the person, and never pass `--yes` without their say.
- Never upgrade a pin by hand: `mise run cloudflare:update`.
- Never edit what `mise run cloudflare:03-init` writes: `.mise/tasks/mise-cloudflare.toml`, `docs/mise-cloudflare/`,
  this skill, `.github/workflows/cloudflare.yml` and the Claude Code hook. After upgrading mise-cloudflare, run
  `mise run cloudflare:03-init`, which writes them again.
- Never weaken a rule `mise run cloudflare:04-check` reports: fix the repo.
