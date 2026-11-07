# mise-cloudflare

[![ci](https://github.com/joeblew999/mise-cloudflare/actions/workflows/ci.yml/badge.svg)](https://github.com/joeblew999/mise-cloudflare/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/joeblew999/mise-cloudflare)](https://github.com/joeblew999/mise-cloudflare/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/joeblew999/mise-cloudflare)](go.mod)
[![license](https://img.shields.io/github/license/joeblew999/mise-cloudflare)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/joeblew999/mise-cloudflare.svg)](https://pkg.go.dev/github.com/joeblew999/mise-cloudflare)

**The problem.** Putting a repo's Cloudflare Worker on Cloudflare properly takes a dozen fiddly steps, and each repo
ends up doing them a little differently, or not at all:

- wrangler and Node at the same versions on every machine, in CI and in a Claude Code cloud session;
- a Cloudflare login for the repo, without API tokens pasted into `.env` files or handed to CI;
- the Worker's secrets set on Cloudflare and kept out of git, locally (`.dev.vars`) and again for previews, which don't
  get the Worker's own;
- what its bindings use (a D1, a KV namespace, a bucket, a queue, …) for production, for local development and for
  each pull request's preview, with no account IDs in the repo;
- Cloudflare building every push: production from `main`, a preview for every pull request, staging if you want it;
- CI that checks the repo without any Cloudflare credential;
- all of that kept up to date as wrangler, Cloudflare and the tools move on.

**What mise-cloudflare does.** It does all of that through [mise](https://mise.jdx.dev), for any repo with a wrangler
config, whatever is in it: the repo's own pinned wrangler reads the config, runs it and deploys it, and mise-cloudflare
never parses one. One command adds it to a repo, and from then on every step is a mise task, numbered in the order you
need them. Each one says what it did and what to run next, and `mise run cloudflare` shows where the repo is at any
time. The logins are wrangler's and cf's own, made once per machine when you click **Allow**; GitHub never holds a
Cloudflare credential. Every version is pinned, and one task, `mise run cloudflare:update`, moves them all, by
[mise-check](https://github.com/joeblew999/mise-check), whose job pins are. CI checks it
on macOS, Linux and Windows, and it sets itself up in a Claude Code cloud session.

## Use it in your repo

Step by step, at the repository's root (GitHub and Claude Code read `.github/` and `.claude/` only there), in a repo
that's new or has a Worker of its own (a wrangler config, and wrangler in its `package.json`):

1. **Install mise**, once on each machine: [mise's getting started](https://mise.jdx.dev/getting-started.html)
   (`curl https://mise.run | sh`, or `brew install mise`).
2. **mise-cloudflare's files, always:**

   ```sh
   mise exec github:joeblew999/mise-cloudflare@latest -- mise-cloudflare init
   ```

   It runs the newest release, or the one you name instead of `latest` (`@0.1.15`: they're all on
   [its releases](https://github.com/joeblew999/mise-cloudflare/releases)); mise takes a new release as `latest` once
   it's a day old. It writes the repo's `.mise/` folder (its tasks, and mise-cloudflare pinned at exactly that release,
   which mise gets from GitHub), its GitHub workflow, its Claude Code hook and skill, its docs and its `.gitignore`.
3. **The template's Worker, only in a repo with none yet:**

   ```sh
   mise run cloudflare:template
   ```

   It writes the template's Worker, named for the repo's folder, with the `mise.toml` that installs its wrangler and a
   README, and installs it. It never writes over a file: a repo with a Worker of its own gets none of it, and installs
   its wrangler its own way.
4. **Then:**

   ```sh
   mise run cloudflare
   ```

   It shows what's done, and what to run next.

Commit what steps 2 and 3 wrote.

`init` writes everything your repo gets, its own docs among them: `docs/mise-cloudflare/README.md`, with every
command, the logins, previews, staging and production. That page is
[templates/docs/mise-cloudflare/README.md](templates/docs/mise-cloudflare/README.md) here, in the template, just as a
repo has it.

## What it does with your Worker's bindings

wrangler does the work for every kind of binding; mise-cloudflare adds only what wrangler leaves undone:

| Step | For every kind, by wrangler | Kind by kind, by mise-cloudflare |
|---|---|---|
| `04-check` | wrangler's own validation of the config: its warnings are problems | |
| Every step that runs the Worker | | The package's own `build` script first, when it has one (`npm run build`, with its lockfile's package manager): `06-serve`, `08-deploy`, `11-preview` and Workers Builds. wrangler then runs what it wrote: Cloudflare's Vite plugin writes the config it deploys, as such a repo's own `deploy` script (`npm run build && wrangler deploy`) does |
| `06-serve` | `wrangler dev`. What wrangler simulates runs locally; what it runs on Cloudflare even locally (its own pick: Workers AI, an AI Search namespace, a binding marked `remote`, …) runs on the repo's login, which it makes first, and needs its resource there | |
| `08-deploy` | `wrangler deploy`, which makes what a binding uses when the config names nothing, of the kinds it can: KV, D1, R2, Queues, Flagship apps, Dispatch Namespaces | |
| `13-delete` | `wrangler delete`, which deletes the Worker's own Durable Objects with it, all their data too, and leaves what deploy made | It says which Durable Objects go when it asks, and checks they went. It deletes what deploy made and named after the Worker: KV, D1, R2, Queues, Flagship apps. What the config names is yours and stays (an AI Search or Agent Memory namespace always is). It names every binding it leaves |
| Previews | `wrangler preview` | Their own of what deploy makes: KV, D1, R2, Queues and Flagship apps, in the Worker's Previews Base |
| Environments | The one `CLOUDFLARE_ENV` names (wrangler's own pick), or the top level | One login for the repo, named after the top level's Worker, whichever environment deploys |

What's proven and where:

- **On Cloudflare, every step:** KV, D1, R2 and a secret (the template); a Queue, a Flagship app and Workers AI (the
  extras, below), each with the previews' own; a wrangler environment, `env.staging` (`new-repo:e2e-env`).
- **A third-party Worker, every step:** cloudflare/realtime-examples' `video-to-jpeg` (a Durable Object, static
  assets, vars), adopted as its owner would, on its own wrangler (`repos:e2e-adopt`).
- **On third-party configs, `check` only:** every config in [repos.txt](repos.txt)'s repos, with Durable Objects,
  Workflows, Hyperdrive, rate limits, containers, Workers AI and static assets among them.
- **Not handled yet:** a Python Worker, which deploys with pywrangler, not wrangler (#17); a repo of several Workers,
  a monorepo (#20).
- **Not yet run:** Workers Builds deploying a named environment (#20); a Dispatch Namespace, which needs Workers for
  Platforms (#23). The open work is the repo's [issues](https://github.com/joeblew999/mise-cloudflare/issues).

The repo's login is a wrangler auth profile, which older wranglers (4.49.0) don't have: on one, `06-serve` still runs
its local bindings, and `04-check` says to move it (`mise run cloudflare:update` moves every pin).

## Working on mise-cloudflare

This repo is two things, kept apart:

| | What | Where |
|---|---|---|
| **For other repos** | The `mise-cloudflare` binary, and the template: a whole repo you can drop anywhere and run, whose tool half `init` writes into a repo (its tasks, its check workflow, its Claude Code hook and skill, its docs, its `.gitignore`) | [templates/](templates/), [cmd/mise-cloudflare/](cmd/mise-cloudflare/), [internal/](internal/) |
| **For this repo** | The tasks for working on it, its tests, the release, its CI. This README | [mise.toml](mise.toml), the `*_test.go` at the top, [.github/workflows/](.github/workflows/) |

Nothing for this repo is ever in `templates/`, and nothing in `templates/` is this repo's own. The Go says it too:
`cmd/mise-cloudflare` is the one entry point that ships, with `internal/` and `templates/`; everything this repo runs for
itself in Go is a test at the top (the `*_test.go`, package `e2e`), which the root's tasks run.

Everything is driven from this repo's [mise.toml](mise.toml), run at the top, and each task runs what it needs first:
run the furthest you need, here, never waiting on GitHub. In this checkout the template's `.mise/config.toml` says
`latest`, and `mise run tool:install` overrides it on this machine only, with this checkout's build.

### Rules

- Work with any wrangler config: the repo's own wrangler reads it (`internal/wconfig`, the one bridge), runs it and
  deploys it. Never parse a config here, and never refuse, change or delete what it names. What wrangler can't answer
  is a gap we fill, kept small.
- Decide every outcome on Cloudflare's HTTP status (`internal/cfapi`; cf's from the status in its error box), never on
  its wording.
- Never create, ask for or store a Cloudflare API token for a person: the repo's login is a wrangler auth profile, and
  Workers Builds deploys with Cloudflare's own build token. CI only checks.
- A login needs a person to click Allow: never try to do it for them. With `CLOUDFLARE_LOGIN=device`, or in a Claude
  Code cloud session, the tool prints a link with the code already in it; send it to the person.
- The repo's login asks for wrangler's own login's scopes, read from the repo's wrangler (`wrangler login
  --scopes-list`): never a list of binding kinds or a binding-to-scope table here. There's no settings file: every
  choice is a task's flag, kept by Cloudflare's Workers Builds setup.
- Test on Cloudflare itself, never against a fake API. The template stays minimal (one small Worker, one D1, KV and R2,
  one secret), so its walks are fast; other kinds are the extras.
- Work with the cloud session through pull request comments, never through the owner copying text between devices.

### Tasks

Every task is named noun:verb, what it acts on and then what it does, as a repo's own are (`cloudflare:08-deploy`).
`mise tasks` lists each with what it does.

| Task | What | When | Cloudflare |
|---|---|---|---|
| `template:setup` | The template's own setup: its pinned wrangler, which the tool's tests read configs with (they skip those without it) | Before `tool:test`, once | No |
| `template:e2e-serve` | The walk on the template, as far as `serve` | After every change | No |
| `template:e2e-deploy` | The walk on the template, as far as `deploy`: about 3 minutes | After a change to how it deploys | Yes |
| `template:e2e-connect-github` | The walk on the template, as far as `connect-github`: about 7 minutes (it waits on Cloudflare's builds) | After a change to `connect-github` | Yes, and GitHub |
| `template:check-mise` | `TestTemplateMise` on the template's mise file, the tool's Go, the walk and the skill, both ways | After a change to a task, a command or the skill | No |
| `template:update` | The template's pins at their newest, by its own `cloudflare:update` (mise-check's update): each moved one before → after | To move its pins; then the walks | No |
| `new-repo:e2e-serve` | The walk on a new repo, as far as `serve` (`--version` pins a release) | After every change | No |
| `new-repo:e2e-deploy` | The walk on a new repo, as far as `deploy` | After a change to how it deploys | Yes |
| `new-repo:e2e-extras` | `new-repo:e2e-deploy` with the extras | After a change to `13-delete`, `06-serve` or previews | Yes |
| `new-repo:e2e-env` | `new-repo:e2e-deploy` in a wrangler environment, `env.staging`, on the repo's one login | After a change to environments or the login | Yes |
| `repos:check [owner/repo]` | The corpus: every wrangler config in third-party repos, read and checked | After a change to how the tool reads a config or what `check` asks of a repo | No |
| `repos:e2e-adopt <path>` | The walk on one of the corpus's Workers, adopted as its owner would (below), as far as `deploy` | After a change to how the tool works on a repo it didn't make | Yes |
| `tool:build` | This checkout's build, `bin/mise-cloudflare` | Any task needs it | No |
| `tool:install` | That build as the mise version `local`, for any repo on this machine (`mise use github:joeblew999/mise-cloudflare@local` there) and the template's (`templates/.mise/config.local.toml`, never committed) | To try a change in another repo, never by waiting on a release | No |
| `tool:test` | `go vet`, the Go tests, the release config | After every change | No |
| `tool:release` | A release from the commit checked out, as `release.yml` runs it; `--dry-run` tags and publishes nothing | Never by hand: a merge to main runs it | GitHub |
| `root:check-mise` | `TestRootMise` on this repo's own `mise.toml`, its mise side: one line each, the Go it runs there, `usage` passed by name, no POSIX shell syntax, so every task runs on Windows too | After a change to this repo's tasks | No |

This repo's pins, and the rules on its mise files, are [mise-check](https://github.com/joeblew999/mise-check)'s, through
its own tasks, which `mise-check init` wrote into `.mise/` (`.mise/config.toml` pins it, at the release `go.mod`
requires its packages at): `mise run mise-check` says what's done and what to run next, and every task is
[docs/mise-check/README.md](docs/mise-check/README.md). Pins move only by `mise run mise-check:update`, then
`tool:test` and the walks prove it.

### Tests

**The walk.** A repo's steps are its own numbered tasks, `cloudflare:03-init` to `cloudflare:15-clean`
([templates/.mise/tasks/mise-cloudflare.toml](templates/.mise/tasks/mise-cloudflare.toml)), after steps 00 to 02 (the
repo's git, `mise install`, its own `setup`). One name all the way through: `cloudflare:08-deploy` runs
`mise-cloudflare deploy`, which is `main.go`'s `case "deploy"` calling `r.deploy` in `deploy.go`. The walk tests (`TestWalkTemplate`, `TestWalkNewRepo`, …, one a kind of repo) walk
them in their order, through the repo's own mise file, as a person takes them, each step with its check, as far as a
stage:

| Stage | As far as | Needs |
|---|---|---|
| `serve` | `06-serve`: the Worker locally, with local D1, KV and R2; on the way, `04-check`'s rules and what the repo's GitHub workflow runs (`cloudflare:verify`) | Nothing |
| `deploy` | `08-deploy` on the repo's own login, the live D1, KV, R2 and secret, stray tokens refused; `11-preview`, the preview's own; then `13-delete` | A Cloudflare account; the first time, an Allow |
| `connect-github` | `09-connect-github`: Workers Builds builds production from a throwaway branch and a preview for a pull request's branch, gone with its branch | GitHub too |

It walks five kinds of repo:

- **The template**, [templates/](templates/), in place; for `connect-github`, in a checkout of this repo at HEAD, as
  Cloudflare builds it from GitHub.
- **A new repo**, made as a person makes one: an empty folder (`mise-cloudflare-new`) with its own git,
  `mise-cloudflare init`, then its own `cloudflare:template`. The walk makes it in a folder of its own, gone when the
  walk ends. Its `init` runs at this checkout's build (`local`), or at a release with `--version`, as the release
  tries it, and pins itself in `.mise/config.toml`. Not `connect-github`: it has no GitHub.
- **A new repo with extras**: the same, whose developer first adds bindings beyond the template's
  ([e2e_bindings_test.go](e2e_bindings_test.go), one row a kind: a Queue, a Flagship app and Workers AI; it says why
  other kinds aren't rows). wrangler deploy must make each one and `13-delete` delete it, checked through Cloudflare's
  API, and `11-preview` make the previews' own and bind it in the preview. Workers AI runs on Cloudflare even locally:
  with no login, in CI, `06-serve` must stop before `wrangler dev` and say it needs the repo's login, and after
  `08-deploy` run on it.
- **A new repo in a wrangler environment** (`new-repo:e2e-env`): the same, whose developer adds `env.staging` (its own
  D1, KV, R2 and secret, which wrangler doesn't inherit); every step runs with `CLOUDFLARE_ENV=staging`. Its Worker,
  `mise-cloudflare-new-staging`, deploys on the repo's one login, named after the top level's Worker, which is never
  deployed.
- **A third-party Worker, adopted** (`repos:e2e-adopt repos/cloudflare/realtime-examples/video-to-jpeg`): its folder,
  as `repos:check` cloned it, copied into a repo of its own with its own git, then `mise-cloudflare init`, its own
  install by its lockfile, and its wrangler moved as the tool says when it has no auth profiles (`mise use aube`,
  `cloudflare:update`). Then the steps on its own wrangler, its checks being what a stranger's Worker can be held to:
  it answers, locally and live, and nothing of it, its Durable Objects among them, is left on Cloudflare.

Each walk first says what it runs: the repo's mise tools, mise-cloudflare's build, the installed wrangler. It runs in
the repo on that repo's own mise only (`MISE_CEILING_PATHS`). A walk that deploys starts by deleting whatever a stopped
one left on Cloudflare, and deletes what it made even when it fails. Besides the steps it runs only
`cloudflare:run-wrangler` and Cloudflare's API, with the login wrangler gives it, to look at the account, decided on
the API's status as the tool decides; the rest it looks at as a person does: the Worker's URL, git, Cloudflare's
builds. Previews, staging and production are shown working by Cloudflare's own checks: never build a harness for
those.

**The corpus** (`repos:check`): the repos named, else [repos.txt](repos.txt)'s, cloned into `repos/` (git-ignored).
It finds every `wrangler.json`, `wrangler.jsonc` and `wrangler.toml` at any depth, reads each in place with our
wrangler, at its top level and in each environment, then runs the tool's own `check` there: one line per config and
environment (its name, compatibility date, bindings, the named and the remote ones marked, secrets, previews, the
wrangler its repo pins, wrangler's warnings) and what `check` finds. Each problem it shows is either our taste, to
remove, or a real fact about that repo. Add a repo to repos.txt to add it to the corpus.

**The mise ↔ Go checker**, [mise_test.go](mise_test.go)'s `TestRootMise` and `TestTemplateMise`, checks a mise file against the Go it runs, both
ways, for any repo (the caller says which). Each task's command is a command's file in `cmd/mise-cloudflare`, and each
command a task's; each task the walk names is one; every task declares its flags and arguments with mise's `usage` and
passes them by name. `root:check-mise` points it at this repo's own `mise.toml`, its mise side only: the root runs Go,
it isn't a binary's tasks.

### Layout

| Path | Whose | What |
|---|---|---|
| [templates/](templates/) | other repos' | the template: a sample Worker plus the tool's half, which `init` writes into a repo at the same paths (the tasks, the workflow, the Claude Code hook and settings, the agent skill, the docs, the `.gitignore`); [templates.go](templates/templates.go) lists that half |
| [cmd/mise-cloudflare/](cmd/mise-cloudflare/) | other repos' | the command: a file per command, `x.go` holding `r.x`, which `main.go`'s `case "x"` calls |
| [internal/](internal/) | other repos' | the login (`wrangler`), Cloudflare's cf (`cf`), Cloudflare's API decided on its status (`cfapi`), the Worker's config as wrangler reads it (`wconfig`), the rules (`rules`) |
| [e2e_walk_test.go](e2e_walk_test.go), [e2e_steps_test.go](e2e_steps_test.go), [e2e_mise_test.go](e2e_mise_test.go), [e2e_cloudflare_test.go](e2e_cloudflare_test.go), [e2e_bindings_test.go](e2e_bindings_test.go) | this repo's | the walk, on [mise-check's `walk`](https://github.com/joeblew999/mise-check#use-walk), which runs the steps in their order and does everything that isn't Cloudflare's: the walk tests and their stages; the steps, each with its check; what's Cloudflare's in making a repo (a new one, an adopted Worker, a checkout of `HEAD`); what it looks at on Cloudflare; the extras |
| [tool_test.go](tool_test.go) | this repo's | `TestToolRelease`, the release: only `mise run tool:release` runs it |
| [repos.txt](repos.txt), `repos/` | this repo's | the corpus's repos, and their clones (git-ignored) |
| [mise_test.go](mise_test.go) | this repo's | `TestRootMise` and `TestTemplateMise`: [mise-check's `misecheck`](https://github.com/joeblew999/mise-check) on a mise file of this repo and the Go it runs, both ways |
| [.mise/](.mise/), [docs/mise-check/](docs/mise-check/), [.claude/](.claude/), [.github/workflows/mise-check.yml](.github/workflows/mise-check.yml) | this repo's | mise-check's, as `mise-check init` writes them into any repo: its tasks and its pin, its docs, its Claude Code hook and skill, its workflow |
| [.github/workflows/](.github/workflows/) | this repo's | its own workflows; other repos get theirs from `templates/` |

### CI and releases

Shell steps only, no JavaScript actions, and no Cloudflare credentials. Each workflow checks out and installs mise,
then only runs the root mise, as on any machine:

- [ci.yml](.github/workflows/ci.yml), on every push and pull request (not the walk's throwaway `e2e-*` branches), on
  Linux, macOS and Windows: `template:setup`, `tool:test`, `template:check-mise`, `root:check-mise`, `repos:check`,
  then `template:e2e-serve` and `new-repo:e2e-serve`.
- [release.yml](.github/workflows/release.yml), on every change to the tool merged to main: `tool:release`. It takes
  the commit's `v*` tag or tags the next patch version, [goreleaser](.goreleaser.yaml) puts the `mise-cloudflare`
  binary for macOS, Linux and Windows on the release, a copy stamped out pinning that release is walked
  (`new-repo:e2e-serve --version`), and the version goes on proxy.golang.org, so `go get` and pkg.go.dev have it at
  once. So the release is always main.
- [new-repo.yml](.github/workflows/new-repo.yml), once a release run has passed (or by hand, for any release): a new
  repo made the README's way at that release, walked as far as `serve`, on Linux, macOS and Windows, its tasks in each
  one's own shell (`cmd` on Windows). It runs after the release, so releases stay fast.
- [mise-check.yml](.github/workflows/mise-check.yml), mise-check's, on every push and pull request:
  `mise-check:verify`, its rules on this repo's pins and mise files.

The walks that deploy run here, not in CI: before merging a change to how it deploys, `template:e2e-deploy`; to
`13-delete` or `06-serve`, `new-repo:e2e-extras`; to `connect-github`, `template:e2e-connect-github`.

This README is the one place for all of it, for people and agents alike: CLAUDE.md only loads it (`@README.md`).
