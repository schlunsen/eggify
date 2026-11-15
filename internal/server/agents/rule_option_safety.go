package agents

import (
	"path/filepath"
	"strings"
)

// A Bash prefix rule such as Bash(git:*) means "let the agent run git", not
// "let the agent run whatever git can be told to run". Many programs take
// options that run another program, load config from elsewhere or write
// arbitrary files: `git -c core.pager='sh -c …' log`, `find . -exec sh …`,
// `tar --to-command=…`, `ssh -o ProxyCommand=…`. A prefix rule never
// auto-approves a command that uses one of them; the user is asked instead,
// which is always safe.
//
// The table is deliberately conservative: it refuses some harmless spellings
// too (e.g. `git show -c`, `rsync -e ssh`). Programs that run code by design
// (python, node, make targets, npm scripts) are as trusted as the project
// files they read, which need their own approval to change.

// codeExecOptionCheck returns a reason when args (the words after the
// program name) make the program run code, load untrusted config or write
// arbitrary files.
type codeExecOptionCheck func(args []string) string

// codeExecOptionChecks maps a program's base name to its check.
var codeExecOptionChecks = map[string]codeExecOptionCheck{
	"git":    gitCodeExecOption,
	"find":   findCodeExecOption,
	"npm":    npmCodeExecOption,
	"pnpm":   pnpmCodeExecOption,
	"yarn":   yarnCodeExecOption,
	"npx":    npxCodeExecOption,
	"tar":    tarCodeExecOption,
	"gtar":   tarCodeExecOption,
	"bsdtar": tarCodeExecOption,
	"rsync":  rsyncCodeExecOption,
	"ssh":    sshCodeExecOption,
	"scp":    sshCodeExecOption,
	"sftp":   sshCodeExecOption,
	"sed":    sedCodeExecOption,
	"gsed":   sedCodeExecOption,
	"awk":    awkCodeExecOption,
	"gawk":   awkCodeExecOption,
	"mawk":   awkCodeExecOption,
	"nawk":   awkCodeExecOption,
	"go":     goCodeExecOption,
	"cargo":  cargoCodeExecOption,
	"make":   makeCodeExecOption,
	"gmake":  makeCodeExecOption,
}

// commandRunsCodeViaOptions reports why words (a parsed simple command) use
// an option that makes the named program run arbitrary code, or "" if none.
func commandRunsCodeViaOptions(words []string) string {
	if len(words) == 0 {
		return ""
	}
	check := codeExecOptionChecks[filepath.Base(words[0])]
	if check == nil {
		return ""
	}
	return check(words[1:])
}

// optionMatches reports whether arg is opt itself or opt=value (long
// options), so "--upload-pack" matches "--upload-pack=x" too. A long option
// also matches when abbreviated ("--upload-pa", "--to-com=sh"): getopt_long
// (GNU tar, make, sed), popt (rsync) and git's parse-options all accept any
// unambiguous prefix.
func optionMatches(arg, opt string) bool {
	if arg == opt || strings.HasPrefix(arg, opt+"=") {
		return true
	}
	if !strings.HasPrefix(arg, "--") || !strings.HasPrefix(opt, "--") {
		return false
	}
	name, _, _ := strings.Cut(arg[2:], "=")
	return name != "" && strings.HasPrefix(opt[2:], name)
}

// anyOption returns the first arg that matches one of opts.
func anyOption(args []string, opts ...string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		for _, o := range opts {
			if optionMatches(a, o) {
				return a
			}
		}
	}
	return ""
}

// shortOption reports whether arg is a short option cluster ("-xvf") that
// contains letter, or that option with its value attached ("-ofoo").
func shortOption(arg string, letter byte) bool {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return false
	}
	return strings.IndexByte(arg[1:], letter) >= 0
}

// ---------------------------------------------------------------------------
// git
// ---------------------------------------------------------------------------

// gitGlobalOptionsWithValue are git's global options that take the next word
// as their value (when not written as --opt=value).
var gitGlobalOptionsWithValue = map[string]bool{"--namespace": true, "--list-cmds": true}

// gitRefusedGlobal are global options that point git at other config, other
// repositories (with their own hooks and config) or other programs.
var gitRefusedGlobal = []string{"--config-env", "--exec-path", "--git-dir", "--work-tree", "--super-prefix", "--config"}

// gitRefusedAnywhere run another program or load config wherever they appear.
var gitRefusedAnywhere = []string{
	"--config", "--config-env", "--upload-pack", "--receive-pack", "--exec",
	"--ext-diff", "--textconv", "--template", "--extcmd", "--open-files-in-pager",
	"--output",
}

// gitRefusedSubcommands run user-supplied commands or programs by design.
var gitRefusedSubcommands = map[string]bool{
	"filter-branch": true, "filter-repo": true, "instaweb": true, "send-email": true,
	"daemon": true, "shell": true, "upload-pack": true, "receive-pack": true,
	"upload-archive": true, "remote-ext": true, "remote-fd": true, "web--browse": true,
	"credential": true, "credential-store": true, "credential-cache": true,
	"difftool": true, "mergetool": true,
}

func gitCodeExecOption(args []string) string {
	// Global options before the subcommand.
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" || !strings.HasPrefix(a, "-") {
			break
		}
		if a == "-C" || strings.HasPrefix(a, "-C") {
			return "git -C runs in another repository, with its own config and hooks"
		}
		if strings.HasPrefix(a, "-c") {
			return "git -c sets config that can run commands (core.pager, alias.*, core.sshCommand, ...)"
		}
		for _, o := range gitRefusedGlobal {
			if optionMatches(a, o) {
				return "git " + o + " changes which config, repository or programs git uses"
			}
		}
		if gitGlobalOptionsWithValue[a] {
			i++
		}
	}
	if i >= len(args) {
		return ""
	}
	sub := args[i]
	rest := args[i+1:]
	if gitRefusedSubcommands[sub] {
		return "git " + sub + " runs other programs"
	}
	for _, a := range rest {
		if a == "--" {
			break
		}
		if a == "-c" || (sub == "clone" && strings.HasPrefix(a, "-c")) {
			return "git " + sub + " -c can set config (clone) that runs commands"
		}
		for _, o := range gitRefusedAnywhere {
			if optionMatches(a, o) {
				return "git " + o + " runs another program, loads config or writes arbitrary files"
			}
		}
		if strings.HasPrefix(a, "-O") && sub == "grep" {
			return "git grep -O opens files with an arbitrary program"
		}
	}
	switch sub {
	case "clone", "fetch", "pull", "ls-remote":
		for _, a := range rest {
			if a == "-u" || strings.HasPrefix(a, "-u") {
				return "git " + sub + " -u names the program to run on the remote side"
			}
		}
	case "rebase":
		for _, a := range rest {
			if a == "-x" || strings.HasPrefix(a, "-x") || a == "-i" || a == "--interactive" {
				return "git rebase --exec / -i runs commands"
			}
		}
	case "bisect":
		if len(rest) > 0 && rest[0] == "run" {
			return "git bisect run runs a command"
		}
	case "submodule":
		for _, a := range rest {
			if a == "foreach" {
				return "git submodule foreach runs a command"
			}
		}
	case "config":
		if !gitConfigIsReadOnly(rest) {
			return "git config writes config that can run commands"
		}
	}
	return ""
}

// gitConfigIsReadOnly reports whether `git config <args>` only reads.
func gitConfigIsReadOnly(args []string) bool {
	if len(args) > 0 && (args[0] == "get" || args[0] == "list") {
		// git >= 2.46: git config get|list ...
		for _, a := range args[1:] {
			if optionMatches(a, "--file") || a == "-f" || optionMatches(a, "--blob") {
				return false
			}
		}
		return true
	}
	read := false
	for _, a := range args {
		switch {
		case a == "--get", a == "--get-all", a == "--get-regexp", a == "--get-urlmatch",
			a == "--list", a == "-l", a == "--get-color", a == "--get-colorbool":
			read = true
		case optionMatches(a, "--file"), a == "-f", optionMatches(a, "--blob"),
			a == "--edit", a == "-e", a == "--add", a == "--unset", a == "--unset-all",
			a == "--replace-all", a == "--rename-section", a == "--remove-section":
			return false
		}
	}
	return read
}

// ---------------------------------------------------------------------------
// other programs
// ---------------------------------------------------------------------------

func findCodeExecOption(args []string) string {
	for _, a := range args {
		switch a {
		case "-exec", "-execdir", "-ok", "-okdir":
			return "find " + a + " runs a command"
		case "-delete":
			return "find -delete removes files"
		case "-fprint", "-fprint0", "-fprintf", "-fls":
			return "find " + a + " writes to a file"
		}
	}
	return ""
}

var npmRefusedOptions = []string{
	"--call", "--script-shell", "--node-options", "--userconfig", "--globalconfig",
	"--init-module", "--onload-script",
}

func npmCodeExecOption(args []string) string {
	if o := anyOption(args, npmRefusedOptions...); o != "" {
		return "npm " + o + " runs or loads other code"
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-c" {
			return "npm -c runs a command"
		}
	}
	if sub := firstNonOption(args); sub == "exec" || sub == "x" || sub == "explore" {
		return "npm " + sub + " runs a command"
	}
	return ""
}

func npxCodeExecOption(args []string) string {
	if o := anyOption(args, "--call", "--package", "--script-shell", "--node-options", "--userconfig", "--globalconfig"); o != "" {
		return "npx " + o + " runs or loads other code"
	}
	for _, a := range args {
		if a == "-c" || a == "-p" {
			return "npx " + a + " runs another command or package"
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
	}
	return ""
}

func pnpmCodeExecOption(args []string) string {
	if sub := firstNonOption(args); sub == "exec" || sub == "dlx" || sub == "x" {
		return "pnpm " + sub + " runs a command"
	}
	if o := anyOption(args, "--config", "--script-shell", "--node-options"); o != "" {
		return "pnpm " + o + " loads other config or code"
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--config.") {
			return "pnpm --config.* sets config that can run code"
		}
	}
	return ""
}

func yarnCodeExecOption(args []string) string {
	if sub := firstNonOption(args); sub == "exec" || sub == "dlx" || sub == "node" {
		return "yarn " + sub + " runs a command"
	}
	return ""
}

// firstNonOption returns the first word that is not an option.
func firstNonOption(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func tarCodeExecOption(args []string) string {
	if o := anyOption(args, "--to-command", "--checkpoint-action", "--use-compress-program",
		"--rsh-command", "--rmt-command", "--info-script", "--new-volume-script", "--index-file"); o != "" {
		return "tar " + o + " runs a command"
	}
	for i, a := range args {
		if shortOption(a, 'I') || shortOption(a, 'F') {
			return "tar -I / -F runs a program"
		}
		// Old-style first argument without a dash: "tar xIf ...".
		if i == 0 && !strings.HasPrefix(a, "-") && strings.ContainsAny(a, "IF") {
			return "tar I / F runs a program"
		}
	}
	return ""
}

func rsyncCodeExecOption(args []string) string {
	if o := anyOption(args, "--rsh", "--rsync-path", "--config", "--write-batch", "--only-write-batch", "--read-batch"); o != "" {
		return "rsync " + o + " runs a program or reads other config"
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if shortOption(a, 'e') {
			return "rsync -e runs a remote shell program"
		}
	}
	return ""
}

func sshCodeExecOption(args []string) string {
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		// -o Option=value (ProxyCommand, LocalCommand, KnownHostsCommand, ...),
		// -F config, -S program (scp/sftp) or control socket, -D (sftp server
		// path) all reach other programs or config.
		for _, l := range []byte{'o', 'F', 'S', 'D'} {
			if shortOption(a, l) {
				return "ssh -" + string(l) + " can run another program or load other config"
			}
		}
	}
	return ""
}

func goCodeExecOption(args []string) string {
	if o := anyOption(args, "-exec", "--exec", "-toolexec", "--toolexec", "-overlay", "--overlay", "-modfile", "--modfile"); o != "" {
		return "go " + o + " runs another program or reads other files"
	}
	return ""
}

func cargoCodeExecOption(args []string) string {
	if o := anyOption(args, "--config", "-Z"); o != "" {
		return "cargo " + o + " changes config (e.g. a runner) that runs programs"
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-Z") {
			return "cargo -Z changes how cargo runs"
		}
	}
	return ""
}

func makeCodeExecOption(args []string) string {
	if o := anyOption(args, "--eval", "--file", "--makefile", "--directory", "--include-dir"); o != "" {
		return "make " + o + " runs other recipes"
	}
	afterDashes := false
	for _, a := range args {
		if a == "--" {
			afterDashes = true // GNU make still applies VAR=value after --
			continue
		}
		if !afterDashes && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			for _, l := range []byte{'E', 'f', 'C', 'I'} {
				if shortOption(a, l) {
					return "make -" + string(l) + " runs other recipes"
				}
			}
			continue
		}
		if isMakeAssignment(a) {
			return "make VAR=value can replace the commands a recipe runs (SHELL, CC, ...)"
		}
	}
	return ""
}

// isMakeAssignment reports whether a make argument is a variable assignment:
// NAME=, NAME:=, NAME::=, NAME+=, NAME?= or NAME!= (make names may contain
// dots and dashes, unlike shell variables).
func isMakeAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := strings.TrimRight(word[:eq], ":+?!")
	if name == "" {
		return false
	}
	for _, r := range name {
		if r == '_' || r == '.' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return !strings.HasPrefix(name, "-")
}

// ---------------------------------------------------------------------------
// sed
// ---------------------------------------------------------------------------

func sedCodeExecOption(args []string) string {
	var scripts []string
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i = len(args)
		case a == "-f" || optionMatches(a, "--file") || (shortOption(a, 'f') && !strings.HasPrefix(a, "-i")):
			return "sed -f runs a script from a file"
		case a == "-l":
			// GNU sed takes a value, BSD sed does not: the script's position
			// is ambiguous.
			return "sed -l makes the script's position ambiguous"
		case a == "-i":
			// BSD sed: -i '' (empty backup suffix).
			if i+1 < len(args) && args[i+1] == "" {
				i++
			}
		case a == "-e":
			if i+1 < len(args) {
				scripts = append(scripts, args[i+1])
				i++
			}
			explicit = true
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
			explicit = true
		case a == "--expression":
			if i+1 < len(args) {
				scripts = append(scripts, args[i+1])
				i++
			}
			explicit = true
		case strings.HasPrefix(a, "-e") && !strings.HasPrefix(a, "--"):
			scripts = append(scripts, strings.TrimPrefix(a, "-e"))
			explicit = true
		case strings.HasPrefix(a, "-"):
			// other options (-n, -i, -E, -r, -s, -z, --posix, ...)
		default:
			if !explicit {
				scripts = append(scripts, a)
				explicit = true
			}
		}
	}
	for _, s := range scripts {
		if !sedScriptIsSafe(s) {
			return "the sed script runs a command (e) or writes a file (w)"
		}
	}
	return ""
}

// sedScriptIsSafe reports whether a sed script has no e / w / W commands and
// no s///e or s///w flags. Anything it cannot follow counts as unsafe.
func sedScriptIsSafe(script string) bool {
	r := []rune(script)
	n := len(r)
	i := 0
	skipSpace := func() {
		for i < n && (r[i] == ' ' || r[i] == '\t' || r[i] == ';' || r[i] == '\n') {
			i++
		}
	}
	// readDelimited reads up to (and past) the unescaped delimiter d.
	readDelimited := func(d rune) bool {
		for i < n {
			if r[i] == '\\' {
				i += 2
				continue
			}
			if r[i] == d {
				i++
				return true
			}
			i++
		}
		return false
	}
	skipAddress := func() bool {
		for i < n {
			switch {
			case r[i] >= '0' && r[i] <= '9', r[i] == '$', r[i] == ',', r[i] == '~', r[i] == '!', r[i] == '+', r[i] == ' ':
				i++
			case r[i] == '/':
				i++
				if !readDelimited('/') {
					return false
				}
				for i < n && (r[i] == 'I' || r[i] == 'M') {
					i++
				}
			case r[i] == '\\':
				if i+1 >= n {
					return false
				}
				d := r[i+1]
				i += 2
				if !readDelimited(d) {
					return false
				}
			default:
				return true
			}
		}
		return true
	}
	toLineEnd := func() {
		for i < n && r[i] != '\n' && r[i] != ';' && r[i] != '}' {
			i++
		}
	}
	for {
		skipSpace()
		if i >= n {
			return true
		}
		if !skipAddress() {
			return false
		}
		skipSpace()
		if i >= n {
			return true
		}
		cmd := r[i]
		i++
		switch cmd {
		case '{', '}', '=', 'd', 'D', 'g', 'G', 'h', 'H', 'l', 'n', 'N', 'p', 'P', 'x', 'z', 'F':
		case 'e', 'w', 'W':
			return false
		case 'q', 'Q', 'L':
			toLineEnd()
		case 'b', 't', 'T', ':', 'r', 'R', 'v':
			toLineEnd()
		case 'a', 'i', 'c':
			// Text up to the end of the line (or the script).
			for i < n && r[i] != '\n' {
				if r[i] == '\\' {
					i++
				}
				i++
			}
		case 's', 'y':
			if i >= n {
				return false
			}
			d := r[i]
			if d == '\\' || d == '\n' {
				return false
			}
			i++
			if !readDelimited(d) || !readDelimited(d) {
				return false
			}
			if cmd == 's' {
				for i < n && r[i] != ';' && r[i] != '\n' && r[i] != '}' {
					switch r[i] {
					case 'e', 'w', 'W':
						return false
					}
					i++
				}
			}
		case '#':
			for i < n && r[i] != '\n' {
				i++
			}
		default:
			return false
		}
	}
}

// ---------------------------------------------------------------------------
// awk
// ---------------------------------------------------------------------------

func awkCodeExecOption(args []string) string {
	var programs []string
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i = len(args)
		case a == "-f" || shortOption(a, 'f') && !strings.HasPrefix(a, "-F"),
			optionMatches(a, "--file"), a == "-E", optionMatches(a, "--exec"),
			a == "-l", strings.HasPrefix(a, "-l"), optionMatches(a, "--load"),
			a == "-i", strings.HasPrefix(a, "-i"), optionMatches(a, "--include"):
			return "awk " + a + " loads a program or extension from a file"
		case a == "-e" || a == "--source":
			if i+1 < len(args) {
				programs = append(programs, args[i+1])
				i++
			}
			explicit = true
		case strings.HasPrefix(a, "--source="):
			programs = append(programs, strings.TrimPrefix(a, "--source="))
			explicit = true
		case a == "-v" || a == "-F":
			i++ // value follows
		case strings.HasPrefix(a, "-"):
		default:
			if !explicit {
				programs = append(programs, a)
				explicit = true
			}
		}
	}
	for _, p := range programs {
		if strings.Contains(p, "system") || strings.ContainsAny(p, "|>") || strings.Contains(p, "getline") {
			return "the awk program runs a command (system, |) or writes a file (>)"
		}
	}
	return ""
}

// pathInsideGitDir reports whether filePath (after "~", ".." and symlinks
// are resolved) has a ".git" component, i.e. is a repository's config, hooks
// or objects. Unresolvable paths count as inside.
func pathInsideGitDir(filePath string) bool {
	fp, err := expandRulePath(filePath)
	if err != nil {
		return true
	}
	for _, p := range []string{fp, resolvePathForMatch(fp)} {
		for _, part := range strings.Split(filepath.ToSlash(p), "/") {
			if strings.EqualFold(part, ".git") {
				return true
			}
		}
	}
	return false
}
