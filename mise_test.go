package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestMise forces one structure on a mise file and the Go its tasks run, so the two can't drift apart and every task
// runs the same on macOS, Linux and Windows. That is all it does: it checks the shape, never what the Go does.
//
// Three sides, each checked against the others:
//
//   - the mise file, the interface: each task is one line that runs something, with the flags and arguments it takes
//     declared in its usage;
//   - the Go its tasks run;
//   - Go that runs the tasks by name, an end-to-end test say (optional, GO_CALLERS).
//
// Used here, it holds a loop that runs both ways, mise to Go and back, several levels deep: the root's mise.toml runs
// Go tests (mise run template:e2e-serve runs TestWalk), which run the template's tasks by name (cloudflare:06-serve),
// which run the tool's Go (mise-cloudflare serve, cmd/mise-cloudflare/serve.go). A task of each mise file runs
// TestMise on it, even on the file that holds that task: root:check-mise checks the mise.toml it's in, and
// template:check-mise checks the template's tasks against the Go they run and against the end-to-end Go that runs
// them, so every link is checked in both directions.
//
// It doesn't prescribe one way to lay things out; it accepts many and holds whichever a repo picks to it. Each command
// of a task's run line (they can be joined by &&, || and ;) is read on its own:
//
//   - Go from its source: go run, go test or go build, on a folder (./cmd/mise-cloudflare), several, a whole tree
//     (./...), or none, the folder the task runs in. Each folder must hold Go; nothing else is asked of its layout,
//     so a binary that is one main.go fits as it is.
//   - A built binary of GO_CMD: <binary> <command>, or <binary> alone for its main.go. Its commands are laid out the
//     way GO_LAYOUT says: file, a file each, <GO_CMD>/<binary>/<command>.go (dashes as underscores) holding the
//     function named for it (set_secrets.go, setSecrets); or folder, a folder each, <GO_CMD>/<binary>/<command>/.
//     Code the commands share isn't a command and is left alone. This is the strictest: every command a task runs
//     must be there, every command must be one a task runs, and every flag must be declared on both sides, in the
//     usage of the tasks running the binary and in its Go (flag.Bool("yes", …)).
//   - Anything else (hugo …, wrangler …, go vet …): logged as not checked, never failed.
//
// The tasks can be in a mise.toml ([tasks.…]) or in a file of tasks only (.mise/tasks/*.toml, or one a mise.toml
// includes); GO_CMD and GO_CALLERS are each optional, so a repo checks as much as it has. Only a mise file whose tasks
// run no Go at all fails outright: there would be nothing to keep in sync.
//
// Every task, however it runs Go:
//
//   - its run and its usage are each one line, "…" or '…': longer logic belongs in Go, not in the mise file. One it
//     can't read fails, rather than being skipped;
//   - it declares the flags and arguments it takes in mise's usage, and its run passes each by name with mise's own
//     templates, which mise fills in before any shell runs: {% if usage.yes %} --yes{% endif %}, "{{usage.branch}}".
//     Everything declared is passed, nothing passed is undeclared, and nothing is read mise's deprecated way,
//     {{flag(…)}};
//   - its run has no POSIX shell syntax (${…}, $name, NAME=value in front of a command), which Windows' cmd doesn't
//     read: variables go in the task's env.
//
// Go that runs tasks (GO_CALLERS) names each in full, "namespace:task", and every name must be a task of the mise
// file. A name glued together from pieces (the namespace's string plus a variable) can't be checked, so it fails, and
// so does a folder of callers that names no task at all.
//
// It knows nothing of any repo. The task that runs it says what to check, as mise run template:check-mise does here:
//
//	MISE_FILE   the mise file (without it, TestMise skips). Its tasks run in its own folder, or, for a
//	            .mise/tasks/*.toml, in the project's root; a task's ./path is read from there
//	GO_CMD      the folder of the built binaries, <GO_CMD>/<binary>/   } for built binaries: both, or neither
//	GO_LAYOUT   file or folder: how a binary's commands are laid out    }
//	GO_CALLERS  the folder of Go that runs the tasks by name (optional)
//
// On the Go side it reads only Go: .go files and Go's flag package. It never passes on nothing, and it logs what it
// checked.
func TestMise(t *testing.T) {
	file, cmd, layout, callers := os.Getenv("MISE_FILE"), os.Getenv("GO_CMD"), os.Getenv("GO_LAYOUT"), os.Getenv("GO_CALLERS")
	if file == "" {
		t.Skip("MISE_FILE is the mise file to check, and the root's task sets it: mise run template:check-mise")
	}
	if (cmd == "") != (layout == "") {
		t.Fatalf("GO_CMD=%q GO_LAYOUT=%q: give both, for tasks that run a built binary, or neither", cmd, layout)
	}
	if layout != "" && layout != "file" && layout != "folder" {
		t.Fatalf("GO_LAYOUT=%q: it's file (a command is <GO_CMD>/<binary>/<command>.go) or folder (<GO_CMD>/<binary>/<command>/)", layout)
	}
	tasks, unreadable, err := readTasks(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range unreadable {
		t.Error(u)
	}
	if len(tasks) == 0 {
		t.Errorf("MISE_FILE=%s has no tasks: did it move?", file)
	}
	g := goSide{cmd: cmd, layout: layout}
	if cmd != "" {
		if g.binaries = binariesIn(cmd); len(g.binaries) == 0 {
			t.Errorf("GO_CMD=%s holds no binary (a folder %s/<binary>/): did the Go move?", cmd, cmd)
		}
	}
	checkMiseToGo(t, file, tasks, g)
	if cmd != "" {
		checkGoToMise(t, file, tasks, g)
	}
	checkUsage(t, file, tasks, g)
	if callers == "" {
		t.Logf("Go → mise: no GO_CALLERS, so no Go that runs %s by name is checked", file)
		return
	}
	checkGoCallsMise(t, file, tasks, callers)
}

// ---- The two forms ----

// goSide is the Go a mise file's built binaries are: their folder (cmd), their layout, and the binaries in it. Empty,
// the mise file runs no built binary of ours.
type goSide struct {
	cmd, layout string
	binaries    []string
}

// ran is what one command of a task's run line runs: a place of Go (a folder, or a binary command's file or folder),
// and the binary when the place is one of ours; or, in neither form, the command, in notGo.
type ran struct {
	place, binary, notGo string
}

// runsOf reads a run line command by command (apart by &&, || and ;), each in one of the two forms:
//
//  1. Go from its source: go run ./path, go test ./path, go build ./path → the folder path/, from dir, the folder the
//     task runs in (./... is all of it, and no path is dir itself)
//  2. a built binary, first after any NAME=value: <binary> <command> → by the layout, the file
//     <cmd>/<binary>/<command>.go (dashes as underscores) or the folder <cmd>/<binary>/<command>/; with no command,
//     the binary itself, <cmd>/<binary>/
//
// Anything else is neither: go tool, go vet, shell, other programs.
func (g goSide) runsOf(run, dir string) []ran {
	var rans []ran
	run = templateTag.ReplaceAllString(run, " ") // what mise fills in isn't a command's words
	for _, command := range regexp.MustCompile(`&&|\|\||;`).Split(run, -1) {
		words := strings.Fields(command)
		for len(words) > 0 && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`).MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		switch {
		case words[0] == "go" && len(words) > 1 && slices.Contains([]string{"run", "test", "build"}, words[1]):
			var paths []string
			for _, w := range words[2:] {
				if w == "." || strings.HasPrefix(w, "./") {
					paths = append(paths, strings.TrimSuffix(strings.TrimSuffix(w, "..."), "/"))
				}
			}
			if len(paths) == 0 {
				paths = []string{"."}
			}
			for _, p := range paths {
				rans = append(rans, ran{place: abs(filepath.Join(dir, p)), binary: g.binaryAt(abs(filepath.Join(dir, p)))})
			}
		case g.cmd != "" && slices.Contains(g.binaries, words[0]):
			place := filepath.Join(g.cmd, words[0])
			if len(words) > 1 && regexp.MustCompile(`^[a-z][a-z-]*$`).MatchString(words[1]) {
				if g.layout == "file" {
					place = filepath.Join(place, strings.ReplaceAll(words[1], "-", "_")+".go")
				} else {
					place = filepath.Join(place, words[1])
				}
			}
			rans = append(rans, ran{place: abs(place), binary: words[0]})
		default:
			rans = append(rans, ran{notGo: strings.TrimSpace(command)})
		}
	}
	return rans
}

// binaryAt is the binary of ours whose folder place is, go run ./cmd/proxy's: "" if none.
func (g goSide) binaryAt(place string) string {
	for _, b := range g.binaries {
		if place == abs(filepath.Join(g.cmd, b)) {
			return b
		}
	}
	return ""
}

// ---- The checks ----

// checkMiseToGo checks mise to Go: every place of Go a task runs is there, and is Go. A task with nothing in either
// form is reported, as not checked; a mise file whose tasks run no Go at all fails.
func checkMiseToGo(t *testing.T, file string, tasks []task, g goSide) {
	t.Helper()
	places, missing, notGo := 0, 0, 0
	for _, task := range tasks {
		for _, r := range g.runsOf(task.run, taskDir(file)) {
			if r.notGo != "" {
				notGo++
				t.Logf("not checked: %s's task %s runs %q, which is neither form: go run|test|build ./path, or a binary of GO_CMD", file, task.name, r.notGo)
				continue
			}
			places++
			if !isGo(r.place) {
				missing++
				t.Errorf("mise → Go: %s's task %s runs %q, so its Go is %s, and there's none there", file, task.name, task.run, displayPath(r.place))
			}
		}
	}
	if len(tasks) > 0 && places == 0 {
		t.Errorf("mise → Go: no task of %s runs Go, in either form: did the Go move, or the tasks?", file)
	}
	t.Logf("mise → Go: %d tasks of %s, running %d places of Go: %d with no Go there; %d commands in neither form, not checked", len(tasks), file, places, missing, notGo)
}

// checkGoToMise checks Go to mise: every entry point of a binary under GO_CMD (commandsIn) is one a task runs. A
// binary itself, its main.go, is in use once any task runs it, in either form.
func checkGoToMise(t *testing.T, file string, tasks []task, g goSide) {
	t.Helper()
	run := map[string]bool{}
	for _, task := range tasks {
		for _, r := range g.runsOf(task.run, taskDir(file)) {
			if r.binary != "" {
				run[r.place] = true
				run[abs(filepath.Join(g.cmd, r.binary))] = true
			}
		}
	}
	commands, unrun := 0, 0
	for _, b := range g.binaries {
		for _, place := range commandsIn(filepath.Join(g.cmd, b), g.layout) {
			commands++
			if !run[abs(place)] {
				unrun++
				t.Errorf("Go → mise: %s is a command of %s (GO_LAYOUT=%s), and no task of %s runs it", displayPath(place), b, g.layout, file)
			}
		}
	}
	t.Logf("Go → mise: %d entry points under %s (GO_LAYOUT=%s), against the tasks of %s: %d no task runs", commands, g.cmd, g.layout, file, unrun)
}

// checkUsage checks the golden rule: a task declares the flags and arguments it takes in mise's usage spec, and its
// run line passes each by name with mise's templates (usage.<name>), and nothing undeclared; then mise refuses anything
// else before the Go runs. Within each task: every name its usage declares is in its run, every usage.<name> its run
// uses is declared, nothing is read mise's old way, {{flag(…)}}, and no run line, a task's with usage or not, has POSIX
// shell syntax, which breaks under Windows' cmd. And flags both ways, at each binary of GO_CMD: every flag a task
// running it declares is one its Go defines (flag.Bool("yes", …)), and every flag its Go defines is declared by such a
// task. A task that takes nothing has no usage, and the Go refuses what it doesn't know.
func checkUsage(t *testing.T, file string, tasks []task, g goSide) {
	t.Helper()
	declared := map[string]map[string]string{} // binary → flag → the task that declares it
	withUsage, flags := 0, 0
	for _, task := range tasks {
		for _, m := range deprecated.FindAllStringSubmatch(task.run, -1) {
			t.Errorf("usage: %s's task %s's run uses {{%s(…)}}, which mise has deprecated and removes in mise 2027.5.0: declare it in usage instead", file, task.name, m[1])
		}
		if m := posix.FindString(task.run); m != "" {
			t.Errorf("usage: %s's task %s's run has POSIX shell syntax, %q, which Windows' cmd doesn't read: pass usage values with mise's templates ({{usage.<name>}}), and set variables in the task's env: %s", file, task.name, m, task.run)
		}
		if task.usage == "" {
			continue
		}
		withUsage++
		names := usageNames(task.usage)
		for _, n := range names {
			if !regexp.MustCompile(`\busage\.` + n.env() + `\b`).MatchString(task.run) {
				t.Errorf("usage: %s's task %s declares %s, and its run doesn't pass it (usage.%s): %s", file, task.name, n, n.env(), task.run)
			}
		}
		for _, m := range regexp.MustCompile(`\busage\.([a-z0-9_]+)`).FindAllStringSubmatch(task.run, -1) {
			if !slices.ContainsFunc(names, func(n usageName) bool { return n.env() == m[1] }) {
				t.Errorf("usage: %s's task %s's run passes usage.%s, which its usage doesn't declare", file, task.name, m[1])
			}
		}
		for _, r := range g.runsOf(task.run, taskDir(file)) {
			if r.binary == "" {
				continue
			}
			for _, n := range names {
				if n.flag {
					flags++
					if declared[r.binary] == nil {
						declared[r.binary] = map[string]string{}
					}
					declared[r.binary][n.name] = task.name
				}
			}
		}
	}
	goFlags := 0
	for _, b := range g.binaries {
		defined := goFlagsIn(filepath.Join(g.cmd, b))
		goFlags += len(defined)
		for f, task := range declared[b] {
			if !slices.Contains(defined, f) {
				t.Errorf("usage, mise → Go: %s's task %s declares the flag --%s, and %s's Go defines no flag %q", file, task, f, b, f)
			}
		}
		for _, f := range defined {
			if _, ok := declared[b][f]; !ok {
				t.Errorf("usage, Go → mise: %s's Go defines the flag --%s, and no task of %s running it declares it in its usage", b, f, file)
			}
		}
	}
	t.Logf("usage: %d tasks of %s with a usage, declaring %d flags of binaries; %d flags in the Go of GO_CMD", withUsage, file, flags, goFlags)
}

// checkGoCallsMise checks the other mapping, Go that runs mise tasks: Go names a task in full, as a string
// ("<namespace>:<task>"), and every name it gives is a task of the mise file. What is a name comes from the tasks
// themselves: their namespaces, the part before the ":". A name glued together from its two parts can't be checked,
// so it fails on one. It reads the .go files of the folder callers, and fails if none of them names a task.
func checkGoCallsMise(t *testing.T, file string, tasks []task, callers string) {
	t.Helper()
	names, namespaces := map[string]bool{}, map[string]bool{}
	for _, task := range tasks {
		names[task.name] = true
		if ns, _, ok := strings.Cut(task.name, ":"); ok {
			namespaces[ns] = true
		}
	}
	files, _ := filepath.Glob(filepath.Join(callers, "*.go"))
	if len(files) == 0 {
		t.Errorf("GO_CALLERS=%s has no .go files: did the Go that runs %s move?", callers, file)
		return
	}
	found, inFiles, wrong := 0, map[string]bool{}, 0
	for ns := range namespaces {
		name := regexp.MustCompile(`\b` + regexp.QuoteMeta(ns) + `:[0-9a-z][a-z0-9-]*`)
		glued := regexp.MustCompile(`"` + regexp.QuoteMeta(ns) + `:"\s*\+`)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, g := range glued.FindAllString(string(b), -1) {
				t.Errorf("Go → mise: %s builds a task name from pieces (%s …), so it can't be checked: write the name in full", f, g)
			}
			for _, n := range name.FindAllString(string(b), -1) {
				found++
				inFiles[f] = true
				if !names[n] {
					wrong++
					t.Errorf("Go → mise: %s names the task %s, which isn't a task of %s", f, n, file)
				}
			}
		}
	}
	if found == 0 {
		t.Errorf("Go → mise: no Go in %s names a task of %s, in full (\"<namespace>:<task>\"): did the Go move?", callers, file)
	}
	t.Logf("Go → mise: %d task names, in %d of the %d Go files in %s, against the tasks of %s: %d not tasks", found, len(inFiles), len(files), callers, file, wrong)
}

// ---- Reading the mise file ----

// task is one mise task: its name, its run line, and its usage, the flags and arguments it declares.
type task struct{ name, run, usage string }

// A task's table is [tasks."name"] or [tasks.name] in a mise.toml, which has other tables too ([tools], [settings],
// [env]), and ["name"] or [name] in a file of tasks only (.mise/tasks/*.toml, or one a mise.toml includes). A file with
// any [tasks.…] table is a mise.toml.
var (
	configTaskHeader = regexp.MustCompile(`^\[tasks\.(?:"([^"]+)"|([A-Za-z0-9_-]+))\]$`)
	tasksFileHeader  = regexp.MustCompile(`^\[(?:"([^"]+)"|([A-Za-z0-9_-]+))\]$`)
	runLine          = regexp.MustCompile(`^run\s*=\s*(.*?)\s*$`)
	usageLine        = regexp.MustCompile(`^usage\s*=\s*(.*?)\s*$`)
)

// readTasks reads a mise file's tasks, each with its run line and its usage, each written on one line, "…" or '…'.
// One written any other way (a list, or a string over several lines) it can't read: it says which, in unreadable,
// rather than skip it.
func readTasks(file string) (tasks []task, unreadable []string, err error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	header := tasksFileHeader
	if regexp.MustCompile(`(?m)^\[tasks\.`).Match(b) {
		header = configTaskHeader
	}
	in := false // in a task's table, not another's
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "[") {
			m := header.FindStringSubmatch(line)
			if in = m != nil; in {
				tasks = append(tasks, task{name: m[1] + m[2]})
			}
			continue
		}
		if !in {
			continue
		}
		last := &tasks[len(tasks)-1]
		for _, field := range []struct {
			line *regexp.Regexp
			to   *string
		}{{runLine, &last.run}, {usageLine, &last.usage}} {
			if m := field.line.FindStringSubmatch(line); m != nil {
				if v, ok := oneLine(m[1]); ok {
					*field.to = v
				} else {
					what := strings.Fields(line)[0]
					unreadable = append(unreadable, fmt.Sprintf("%s: the task %s's %s isn't one line (%s = %s): the checker reads one written on one line, \"…\" or '…'", file, last.name, what, what, m[1]))
				}
			}
		}
	}
	return tasks, unreadable, nil
}

// oneLine is the text of a TOML string written on one line, "…" or '…', and whether it is one.
func oneLine(v string) (string, bool) {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' && !strings.HasPrefix(v, `"""`) ||
		v[0] == '\'' && v[len(v)-1] == '\'' && !strings.HasPrefix(v, `'''`)) {
		return v[1 : len(v)-1], true
	}
	return "", false
}

// taskDir is the folder a mise file's tasks run in, as mise runs them: a mise.toml's own folder, or for a file in
// .mise/tasks/ the project's, two folders up. A task's ./path is from there.
func taskDir(file string) string {
	dir := filepath.Dir(file)
	if filepath.Base(dir) == "tasks" && filepath.Base(filepath.Dir(dir)) == ".mise" {
		return filepath.Dir(filepath.Dir(dir))
	}
	return dir
}

// deprecated is mise's old way of reading flags and arguments in a run line, {{arg(…)}}, {{option(…)}} and
// {{flag(…)}}, which mise has deprecated for usage, and removes in mise 2027.5.0.
// posix is shell syntax that only a POSIX shell reads: ${…}, $name, and NAME=value in front of a command.
var posix = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*|(?:^|&&|\|\||;)\s*[A-Za-z_][A-Za-z0-9_]*=\S*\s`)

// templateTag is a mise template tag in a run line, {% … %} or {{ … }}, which mise fills in before the shell runs.
var templateTag = regexp.MustCompile(`\{%.*?%\}|\{\{.*?\}\}`)

var deprecated = regexp.MustCompile(`\{\{-?\s*(arg|option|flag)\(`)

// usageName is a flag or an argument a usage spec declares, by its long name.
type usageName struct {
	name string
	flag bool
}

func (n usageName) String() string {
	if n.flag {
		return "the flag --" + n.name
	}
	return "the argument " + n.name
}

// env is the name as mise's templates have it, usage.<env> (and its environment, usage_<env>): dashes as underscores.
func (n usageName) env() string { return strings.ReplaceAll(n.name, "-", "_") }

// usageNames are the flags (flag "-y --yes", flag "--branch <branch>") and arguments (arg "<name>", arg "[args]...") a
// usage spec declares.
func usageNames(usage string) []usageName {
	var names []usageName
	for _, m := range regexp.MustCompile(`\bflag "(?:-[A-Za-z] )?--([a-z][a-z0-9-]*)`).FindAllStringSubmatch(usage, -1) {
		names = append(names, usageName{m[1], true})
	}
	for _, m := range regexp.MustCompile(`\barg "[<\[]([a-z][a-z0-9_-]*)[>\]]`).FindAllStringSubmatch(usage, -1) {
		names = append(names, usageName{m[1], false})
	}
	return names
}

// ---- Reading the Go ----

// binariesIn are the binaries the Go builds: the folders under cmd.
func binariesIn(cmd string) []string {
	entries, _ := os.ReadDir(cmd)
	var bins []string
	for _, e := range entries {
		if e.IsDir() {
			bins = append(bins, e.Name())
		}
	}
	return bins
}

// commandsIn are a binary's commands, its entry points, by the layout: each folder in its folder (folder), or each .go
// file of its folder that holds the function named for it, as set_secrets.go holds setSecrets (file). Go reserves two
// names, so two files go by their names alone: main.go, the binary itself, and init.go, the command init, whose
// function can't be named init. Code the commands call further down, like a file of what several share, isn't one:
// the checker leaves it alone.
func commandsIn(dir, layout string) []string {
	entries, _ := os.ReadDir(dir)
	var commands []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case layout == "folder" && e.IsDir():
			commands = append(commands, filepath.Join(dir, name))
		case layout == "file" && name == "main.go":
			commands = append(commands, dir) // the binary itself, in use once any task runs it
		case layout == "file" && name == "init.go":
			commands = append(commands, filepath.Join(dir, name)) // Go reserves init: the command's function can't be named for it
		case layout == "file" && !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go"):
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil && entryFunc(strings.TrimSuffix(name, ".go")).Match(b) {
				commands = append(commands, filepath.Join(dir, name))
			}
		}
	}
	return commands
}

// entryFunc finds, in a command's file, the function named for it: set_secrets is setSecrets, a function or a method.
func entryFunc(stem string) *regexp.Regexp {
	words := strings.Split(stem, "_")
	name := words[0]
	for _, w := range words[1:] {
		name += strings.ToUpper(w[:1]) + w[1:]
	}
	return regexp.MustCompile(`(?m)^func (\([^)]*\) )?` + regexp.QuoteMeta(name) + `\(`)
}

// goFlagsIn are the flags the Go in dir defines with the flag package: flag.Bool("yes", …), flags.String("branch", …),
// flag.BoolVar(&yes, "yes", …).
func goFlagsIn(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	var defined []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range regexp.MustCompile(`\.(?:Bool|String|Int|Int64|Uint|Uint64|Float64|Duration)(?:Var\([^,]+,\s*|\()"([a-z][a-z0-9-]*)"`).FindAllStringSubmatch(string(b), -1) {
			if !slices.Contains(defined, m[1]) {
				defined = append(defined, m[1])
			}
		}
	}
	return defined
}

// isGo reports whether place is a .go file, or a folder with a .go file of its own.
func isGo(place string) bool {
	if strings.HasSuffix(place, ".go") {
		st, err := os.Stat(place)
		return err == nil && !st.IsDir()
	}
	entries, err := os.ReadDir(place)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return !e.IsDir() && strings.HasSuffix(e.Name(), ".go") })
}

// abs is place by its full path, so places compare whatever folder they were named from.
func abs(place string) string {
	if a, err := filepath.Abs(place); err == nil {
		return a
	}
	return place
}

// displayPath is place as the checker says it: from here when it's under here, else in full.
func displayPath(place string) string {
	wd, _ := os.Getwd()
	if r, err := filepath.Rel(wd, place); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return place
}

// ---- The checker's own tests: what it reads, and how ----

// TestReadTasks: every way mise writes a task is read, or said to be unreadable, and no other table is taken for one.
func TestReadTasks(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name, text string
		want       []task
		unreadable int
	}{
		{"mise.toml", `[tools]
go = "1.27"

[settings]
skills.fetch = false

[env]
A = "b"

[tasks."a:b"]
run = "mise-cloudflare b"

[tasks.setup]
usage = 'flag "--yes" help="do not ask"'
run = 'aube install{% if usage.yes %} --yes{% endif %}'

[tasks.list]
run = ["a", "b"]

[tasks.lines]
run = """
a
"""
`, []task{{"a:b", "mise-cloudflare b", ""}, {"setup", "aube install{% if usage.yes %} --yes{% endif %}", `flag "--yes" help="do not ask"`}, {"list", "", ""}, {"lines", "", ""}}, 2},
		{"tasks.toml", `["a:b"]
run = "x"

[setup]
usage = '''
flag "--yes"
'''
run = 'y'
`, []task{{"a:b", "x", ""}, {"setup", "y", ""}}, 1},
	} {
		file := filepath.Join(dir, c.name)
		os.WriteFile(file, []byte(c.text), 0o644)
		got, unreadable, err := readTasks(file)
		if err != nil || !slices.Equal(got, c.want) || len(unreadable) != c.unreadable {
			t.Errorf("%s: %+v, %d unreadable (%q), %v; want %+v, %d unreadable", c.name, got, len(unreadable), unreadable, err, c.want, c.unreadable)
		}
	}
}

// TestRunForms: a run line's commands are read in the two forms, and anything else is neither.
func TestRunForms(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cmd", "tool"), 0o755)
	g := goSide{cmd: filepath.Join(dir, "cmd"), layout: "file", binaries: []string{"tool"}}
	in := func(p ...string) string { return abs(filepath.Join(append([]string{dir}, p...)...)) }
	for run, want := range map[string][]ran{
		"tool set-secrets":                                         {{place: in("cmd", "tool", "set_secrets.go"), binary: "tool"}},
		"tool{% if usage.json %} --json{% endif %}":                {{place: in("cmd", "tool"), binary: "tool"}},
		`tool deploy{% for a in usage.args %} "{{a}}"{% endfor %}`: {{place: in("cmd", "tool", "deploy.go"), binary: "tool"}},
		"tool":                            {{place: in("cmd", "tool"), binary: "tool"}},
		"CI=1 tool check --json":          {{place: in("cmd", "tool", "check.go"), binary: "tool"}},
		"go run ./cmd/tool -x":            {{place: in("cmd", "tool"), binary: "tool"}},
		"go test ./e2e -run '^TestX$' -v": {{place: in("e2e")}},
		"go test ./...":                   {{place: in()}},
		"go vet ./... && go test ./...":   {{notGo: "go vet ./..."}, {place: in()}},
		"go tool tool dev":                {{notGo: "go tool tool dev"}},
		"hk check --all":                  {{notGo: "hk check --all"}},
		"echo tool":                       {{notGo: "echo tool"}},
	} {
		if got := g.runsOf(run, dir); !slices.Equal(got, want) {
			t.Errorf("%q: %+v, want %+v", run, got, want)
		}
	}
}
