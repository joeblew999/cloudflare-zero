---
name: mise-check
description: Use whenever you add, rename or change a mise task or the Go command it runs, upgrade anything the repo pins (mise tools, Node packages, Go and Go modules, majors included), or check what its GitHub workflow runs. The repo uses mise-check, at the version its .mise/config.toml pins, and every command is a task, mise run mise-check:<command>. It keeps the mise tasks, the Go they run, the end-to-end tests that run them and this skill in step, both ways. Never upgrade a pin by hand, and never let a task and its Go drift apart.
---

# This repo's mise tasks, their Go, and its pins

This repo uses mise-check (github.com/joeblew999/mise-check) at the version its `.mise/config.toml` pins. It does two
jobs: it keeps the repo's mise tasks and the Go they run in step, and it moves every pin the repo has. Everything, with
each command's details, is in [docs/mise-check/README.md](../../../docs/mise-check/README.md).

Start with `mise run mise-check`: what's done, and what to run next.

## The tasks

Each runs the mise-check command of its name (`mise run mise-check:check` runs `mise-check check`).

| Task | Run it | What it does |
|---|---|---|
| `mise run mise-check` | first, and whenever you're unsure | what's done for the repo, and what to run next |
| `mise run mise-check:init` | after upgrading mise-check | writes mise-check's files again: its tasks, the workflow, the skill, the docs, the hook |
| `mise run mise-check:check` | after any change to a mise file or a skill; when it fails, fix the side it names | the repo's rules: its mise files by misecheck, its skills against their tasks, mise-check's files current, its two pins (the binary in mise, the packages in go.mod) at one release |
| `mise run mise-check:show` | to see where the repo is | the same as mise run mise-check |
| `mise run mise-check:verify` | before every push | what GitHub runs: each pin consistent with what pins it, the repo's setup and test, then check |
| `mise run mise-check:update` | when the person asks to upgrade everything | every pin at its newest: the three below, in order, then mise-check's two pins at one release |
| `mise run mise-check:update-mise` | to upgrade the mise tools only | the tools in the repo's mise files |
| `mise run mise-check:update-packages` | to upgrade the Node packages only | package.json and its lockfile, through aube |
| `mise run mise-check:update-go` | to upgrade Go only | every go.mod's toolchain line and modules, majors too, their import paths rewritten |
| `mise run mise-check:show-users` | to see which of the owner's repos use this one, and which are behind | which of the owner's repos use it, at what version, the behind ones marked |
| `mise run mise-check:template` | only in a repo with no Go program yet | the template's little app, its go.mod and packages; it never writes over a file |
| `mise run mise-check:new-tool` | to start a new mise tool repo, with its binary, template, walks and CI | a new mise tool repo in an empty folder: mise-check at its release, copied and renamed for the module, its packages imported instead |
| `mise run mise-check:show-version` | to say which mise-check this is | its version |
| `mise run mise-check:show-help` | to list every command | every command and its flags |

Flags, each declared in the task's usage and passed by name:

- `--json`: one JSON document on stdout instead of text, on `mise run mise-check`, `show`, `check`, the four updates,
  `show-users` and `show-version`. Read that, never the text: the text may change, the JSON keeps its fields. The
  updates' JSON is every pin with its file, `before`, `after`, `moved` and `major`.
- `--from <folder>`: on new-tool, copy that checkout of mise-check instead of its release, to try a change before it is released. new-tool takes the new repo's module, `github.com/<owner>/<name>`, and runs in an empty folder.
- `--no-major`: on the four updates, no major moves: packages within their ranges, Go modules within their major.

A command given a flag it doesn't take refuses it. The updates never run in CI: CI only checks.

After any update, run `mise run mise-check:verify`: a major move can break the repo's own code, and that's where it
shows. Fix the code; never move the pin back by hand. Then `git diff` shows exactly what moved, and the update's last
lines (or its `--json`) list every pin, before → after.

## Keeping tasks, Go, tests and this skill in step

`mise run mise-check:check` holds the repo's mise files and skills to one structure, so nothing drifts:

- every task's `run` and `usage` one line, each flag declared in `usage` and passed by name
  (`{% if usage.json %} --json{% endif %}`), no POSIX shell syntax (`$name`, `NAME=value cmd`): variables go in the
  task's `env`, longer logic in Go;
- every skill (`.claude/skills/<namespace>/SKILL.md`) names every task and flag of its namespace, and only tasks there
  are.

So when you add, rename or remove a task, change its skill with it, then run the check; when it fails, fix the side it
names. Never weaken the check, skip it, or work around a rule (a shell script, a second line).

A repo with a binary of its own also holds its tasks to its Go commands and to its end-to-end tests: how to wire that,
misecheck's `Config` tests and the end-to-end walks (`walk`), is in [docs/mise-check/README.md](../../../docs/mise-check/README.md).

## Never

- Never upgrade a pin by hand (`go get -u`, `npm install x@latest`, `aube add`, `mise use`, editing a version): run
  the update for its kind, so every pin moves the same way, past each tool's minimum age, and the run lists what moved.
- Never run `npm`, `npx`, `pnpm` or `yarn`: the packages are aube's, at the version the repo's mise pins.
- Never add a task without its Go, its test and its line in the skill, nor a command without its task.
- Never edit what `mise run mise-check:init` writes: `.mise/tasks/mise-check.toml`, `docs/mise-check/`, this skill,
  `.github/workflows/mise-check.yml` and the Claude Code hook. After upgrading mise-check, run
  `mise run mise-check:init`, which writes them again.
- Never weaken a rule `mise run mise-check:check` reports: fix the repo.
