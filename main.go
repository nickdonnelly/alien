package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
)

const version = "0.1.0"

//go:embed shell/alien.zsh
var zshInit string

//go:embed shell/alien.bash
var bashInit string

func main() {
	args := os.Args[1:]

	// Pull --prev-cmd out of args before dispatch. The shell wrapper passes
	// the user's previous command via this flag so `alien <name>` (with no
	// other args) can use it to populate the new alias.
	var prevCmd string
	args, prevCmd = extractFlag(args, "--prev-cmd")

	if len(args) == 0 {
		printHelp()
		return
	}

	switch args[0] {
	case "-h", "--help", "help":
		printHelp()
	case "-v", "--version", "version":
		fmt.Printf("alien %s\n", version)
	case "add":
		cmdAdd(args[1:], prevCmd)
	case "chain":
		cmdChain(args[1:])
	case "ls", "list":
		cmdList(args[1:])
	case "show":
		cmdShow(args[1:])
	case "fzf", "fzf-list":
		cmdFzfList(args[1:])
	case "tab", "tabs":
		cmdTab(args[1:])
	case "get":
		cmdGet(args[1:])
	case "export":
		cmdExport(args[1:])
	case "rm", "delete", "del":
		cmdDelete(args[1:])
	case "toggle":
		cmdToggle(args[1:])
	case "enable":
		cmdEnable(args[1:])
	case "disable":
		cmdDisable(args[1:])
	case "comment":
		cmdComment(args[1:])
	case "edit":
		cmdEdit(args[1:])
	case "init", "shell-init":
		cmdInit(args[1:])
	case "path":
		fmt.Println(storePath())
	case "import-shell":
		cmdImportShell(args[1:])
	case "promote":
		cmdPromote(args[1:])
	case "ufo", "ufos", "pack", "packs":
		cmdUfo(args[1:])
	case "sync":
		cmdSync(args[1:])
	case "run":
		cmdRun(args[1:])
	case "suggest":
		cmdSuggest(args[1:])
	case "skill":
		cmdSkill(args[1:])
	case "stats":
		cmdStats(args[1:])
	case "doctor":
		cmdDoctor(args[1:])
	default:
		// Default: alien <name> [-c "..." -m "..."] is treated as add.
		// Reject anything that *looks* like a subcommand typo to avoid
		// accidentally creating bogus aliases.
		if strings.HasPrefix(args[0], "-") {
			errorf("unknown flag %q", args[0])
			os.Exit(2)
		}
		cmdAdd(args, prevCmd)
	}
}

// extractFlag removes --flag VALUE or --flag=VALUE from args and returns the
// captured value. If the flag is not present, val is "".
func extractFlag(args []string, flag string) ([]string, string) {
	out := make([]string, 0, len(args))
	val := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == flag:
			if i+1 < len(args) {
				val = args[i+1]
				i++
			}
		case strings.HasPrefix(a, flag+"="):
			val = strings.TrimPrefix(a, flag+"=")
		default:
			out = append(out, a)
		}
	}
	return out, val
}

func printHelp() {
	help := `` + brcyan("👽 alien") + ` ` + dim("— quick command-line aliases") + `

` + bold("USAGE") + `
  alien <name>                    add an alias from your previous command
  alien <name> -c "command"       add an alias with an explicit command
  alien <name> -m "comment"       add an alias with a description
  alien chain <name>              pick recent commands from history; chain with &&

` + bold("MANAGE") + `
  alien list                      pretty list of all aliases
  alien show <name>               show details for one alias
  alien edit <name>               edit in $EDITOR
  alien comment <name> <text>     set/clear the comment on an alias
  alien toggle <name>             enable/disable
  alien enable|disable <name>
  alien delete <name>             remove (with confirmation)
  alien promote <name>            take ownership of a shell-imported alias

` + bold("UFO PACKS") + `
  alien ufo list                  installed + built-in available
  alien ufo show <pack>           preview pack contents
  alien ufo install <pack|path>   browse and install (Bubble Tea TUI)
  alien ufo uninstall <pack>      remove a pack's aliases
  alien ufo create <name>         build a pack from your user aliases
  alien ufo export <pack>         dump installed pack to JSON

` + bold("SYNC") + `
  alien sync init <repo-url>      version-control + cross-machine sync
  alien sync push|pull|status     manage the alien data dir as a git repo
  alien sync auto on|off          auto-pull on shell startup, auto-push on save

` + bold("AGENT MODE") + `
  alien run <name> [args]         execute an alias by reference (no shell needed)
  alien ls --json                 machine-readable listing for AI agents
  alien suggest "<command>"       find an alias matching a verbatim command
  alien skill install             install the agent skill into Claude/Cursor/Codex/...
  alien skill targets             list available agent-skill install targets

` + bold("INSIGHT") + `
  alien stats [--top N]           usage tracking (top-N most-used, never-used)
  alien doctor                    self-diagnostic: hook? fzf? sync? skills?

` + bold("SHELL") + `
  alien init zsh|bash             print integration code for your shell
  alien export                    print sourceable alias lines
  alien get <name>                print just the command (used by widget)

` + bold("INSTALL THE SHELL HOOK") + `
  zsh:    ` + cyan(`echo 'source <(alien init zsh)' >> ~/.zshrc`) + `
  bash:   ` + cyan(`echo 'eval "$(alien init bash)"' >> ~/.bashrc`) + `

  Once installed, the ` + bold("a") + ` shortcut and the fuzzy finder
  (default keybind ` + bold("Ctrl-G") + `) become available.
`
	fmt.Print(help)
}
