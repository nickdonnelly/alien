# 👽 alien

> Quick command-line aliases. Run a command, like it, type `alien <name>`, done.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
![Shells](https://img.shields.io/badge/shells-zsh%20%7C%20bash-blueviolet)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)

`alien` turns your shell history into reusable aliases without leaving the
prompt. Run a command, decide it's worth keeping, type `alien <name>`, and
the alias is live in your current shell. No rc-file editing, no `exec zsh`.


---

## Highlights

- **One-keypress aliasing** — `alien <name>` reads the previous command from
  shell history, stores it, and sources it into the running shell.
- **Fuzzy picker** — press <kbd>Ctrl-G</kbd> (or run `a`) to browse every
  alias you have. Search by name, command, or comment. Hit <kbd>Enter</kbd>
  to run, <kbd>Tab</kbd> to insert the alias name into your prompt.
- **Tabs** — <kbd>[</kbd> / <kbd>]</kbd> in the picker switch between
  *all* / *user* / *shell* and one tab per installed pack.
- **UFO packs** 🛸 — install topical bundles of aliases (docker, git, files,
  nav) with conflict-aware select/rename in a Bubble Tea TUI. Build and
  share your own packs.
- **Imports your existing rc aliases** — anything in your `.zshrc`,
  `.bashrc`, oh-my-zsh plugins, etc. is indexed for search; alien shows
  *which file* each came from in the FROM column.
- **Optional git sync** — version-control your aliases and sync them
  across machines. `alien sync init <repo>` and you're done.
- **Fast** — pure-Go binary, no external deps beyond
  [fzf](https://github.com/junegunn/fzf). Picker renders in milliseconds
  even with hundreds of aliases.
- **zsh and bash** — works on macOS bash 3.2 too, no upgrade required.

## Install

### From source

```sh
git clone https://github.com/nick-donnelly/alien
cd alien
./install.sh
```

`install.sh` builds the binary, installs it to `~/.local/bin/alien`, and
(with your confirmation) appends the shell hook to your rc file.

### Manual

```sh
go build -o alien .
mv alien ~/.local/bin/

# zsh:
echo 'source <(alien init zsh)' >> ~/.zshrc

# bash (works on the macOS-default bash 3.2 too):
echo 'eval "$(alien init bash)"' >> ~/.bashrc
```

### Requirements

- Go 1.24+ (build only)
- `fzf` (for the fuzzy picker — install via your package manager)
- `git` (only for `alien sync`)

## Usage

```text
alien <name>                add an alias from the command you just ran
alien <name> -c "cmd"       add an explicit alias
alien <name> -m "comment"   describe what it does
a <name>                    short form of `alien <name>`

alien list                  pretty list of all aliases
alien show <name>           details for one alias
alien edit <name>           open in $EDITOR (rename, retarget, toggle)
alien comment <name> ...    set or clear the comment
alien toggle <name>         enable/disable
alien delete <name>         remove (with confirmation)
alien promote <name>        take ownership of a shell-imported alias
```

### The picker

Press <kbd>Ctrl-G</kbd> or run `a` to open it.

```
ALIAS         COMMAND                              FROM
❯ all  user  shell  🛸 docker  🛸 git
enter:run · tab:insert · ctrl-e:edit · ctrl-d:delete · [/]:tabs · esc:cancel
› ● ll      ls -alh                              .zshrc
  ● gst     git status                           🛸 git    # status
  ● dps     docker ps                            🛸 docker # running
  ● foo     echo foo
```

| Key                              | Action                                    |
|----------------------------------|-------------------------------------------|
| type                             | fuzzy-search across name, command, FROM   |
| <kbd>Enter</kbd>                 | run the alias's command in this shell     |
| <kbd>Tab</kbd>                   | insert the alias name into the prompt     |
| <kbd>Ctrl-E</kbd>                | open the alias for editing in `$EDITOR`   |
| <kbd>Ctrl-D</kbd>                | delete (with confirmation)                |
| <kbd>[</kbd> / <kbd>]</kbd>      | cycle tabs (all / user / shell / packs)   |
| <kbd>Esc</kbd>                   | cancel                                    |

> ℹ️ <kbd>Ctrl-E</kbd>/<kbd>Ctrl-D</kbd> instead of plain `e`/`d` —
> fzf reads letter keys as filter input, so using them as actions would
> break fuzzy search.

### UFO packs

A *pack* is a small bundle of related aliases. alien ships with four:

| Pack     | What's inside                                                       |
|----------|---------------------------------------------------------------------|
| `git`    | `gst`, `gco`, `gcb`, `gp`, `gpl`, `gl`, `glg`, `gd`, `gsta`, ...    |
| `docker` | `dps`, `dpsa`, `di`, `dlog`, `dexec`, `dcu`, `dcd`, `dcl`, ...       |
| `files`  | `ll`, `la`, `lt`, `lsize`, `mkp`, `cpv`, `mvv`, `duh`, ...           |
| `nav`    | `..`, `...`, `....`, `cdtmp`, `cddl`, `cdp`, ...                    |

```sh
alien ufo list                        # built-in + installed
alien ufo show docker                 # preview pack contents
alien ufo install docker              # interactive TUI: select / rename / install
alien ufo install ./mypack.ufo.json   # install from a file
alien ufo install https://…/x.ufo.json
alien ufo uninstall docker            # remove only the entries this pack added
alien ufo create mypack > mypack.ufo.json  # turn your user-aliases into a pack
alien ufo export docker               # dump an installed pack back to JSON
```

The interactive installer (Bubble Tea) lets you toggle individual aliases,
sees and warns about conflicts (with your aliases, shell aliases, or other
installed packs), and lets you press <kbd>r</kbd> to rename on the spot.

#### Pack file format

```json
{
  "ufo": {
    "name": "mypack",
    "version": "1.0.0",
    "description": "A small bundle",
    "author": "you"
  },
  "aliases": {
    "ll":  { "command": "ls -alh", "comment": "long listing" },
    "gst": { "command": "git status", "comment": "status" }
  }
}
```

We'd love community packs — open a PR with a new file under `packs/`.

### Sources: user · shell · pack

Every alias has a *source*. The picker badges them:

- **user** — you added it via `alien <name>`. No badge.
- **shell** — defined in your rc / oh-my-zsh plugin. Badge shows the file
  it came from (`.zshrc`, `omz:git`, etc.). alien refuses to delete or
  edit these — they live in your rc, not here. Run
  `alien promote <name>` to take ownership.
- **pack:`<name>`** — installed via `alien ufo install`. Badge: 🛸 *name*.
  Cleanly removed by `alien ufo uninstall`.

### Sync (optional)

`alien sync` makes `$ALIEN_HOME` a git working tree, so you get version
control *and* cross-machine sync from one feature.

```sh
alien sync init <repo-url>   # set up; pulls if remote has content,
                             # otherwise pushes the local state
alien sync push [-m "msg"]   # commit + push
alien sync pull              # pull --rebase, surface conflicts
alien sync status            # short status
alien sync auto on           # auto-pull on shell startup,
                             # auto-push after changes (debounced)
alien sync auto off
alien sync forget            # disconnect (rm .git, sync.json)
```

Only `aliases.json` is tracked. Generated files (`aliases.sh`, sync state)
are gitignored. Conflicts surface as standard git conflict markers in
`aliases.json`; resolve them in your editor and `alien sync push`.

### Configuration

Set these before sourcing the hook (e.g. in `~/.zshrc`):

| Variable          | Default                       | Purpose                                |
|-------------------|-------------------------------|----------------------------------------|
| `ALIEN_HOME`      | `$XDG_CONFIG_HOME/alien`      | where `aliases.json` lives             |
| `ALIEN_KEYBIND`   | `^G` (zsh) / `\C-g` (bash)    | picker keybinding; empty disables      |
| `ALIEN_FZF_OPTS`  | (empty)                       | extra flags forwarded to fzf           |
| `ALIEN_RC_FILES`  | auto-detected                 | colon-separated rc files to scan       |

## Storage

Aliases live in `$ALIEN_HOME/aliases.json` (default `~/.config/alien/`).
The binary regenerates `aliases.sh` from that on every change; the shell
hook sources it, so new aliases are live immediately in the running shell.

| File             | Purpose                                                         |
|------------------|-----------------------------------------------------------------|
| `aliases.json`   | source of truth (atomic writes)                                 |
| `aliases.sh`     | generated cache; sourced by your shell hook                     |
| `sync.json`      | sync remote URL + auto-sync flags (gitignored)                  |
| `.last_pull`     | throttle marker for auto-pull (gitignored)                      |
| `.tab`           | active picker tab (per-shell-session)                           |
| `.git/`          | only present after `alien sync init`                            |

## How it works

A Go binary owns storage, queries, packs, sync. A small shell script (zsh
or bash) wires it into your shell:

- captures your previous command via `fc -ln -1` so `alien <name>` can
  read it
- sources the regenerated `aliases.sh` on every alien call so new aliases
  go live immediately
- pipes `alias` into `alien import-shell` on every shell start so the
  picker can search rc-defined aliases too
- binds <kbd>Ctrl-G</kbd> to a ZLE / readline widget that runs the fzf
  picker and applies the chosen action (run / insert / edit / delete)

The pack browser is a [Bubble Tea](https://github.com/charmbracelet/bubbletea)
TUI; the fuzzy picker is plain [fzf](https://github.com/junegunn/fzf)
with custom keybindings.

## Contributing

Issues and PRs welcome.

- **New built-in packs** — drop a `name.ufo.json` into `packs/` and open
  a PR. Keep them small, opinionated, and well-described.
- **Bug reports** — please include `alien --version`, your shell, fzf
  version, and a reproduction.
- **Patches** — `go build` and the existing smoke-test paths must keep
  working: `alien fzf`, `alien tab next/prev`, `alien ufo install -y`,
  `alien sync init` against a local bare repo.

```sh
git clone https://github.com/nick-donnelly/alien
cd alien
go build -o alien .
go vet ./...
```

## Uninstall

```sh
make uninstall                  # remove the binary
rm -rf ~/.config/alien          # remove your aliases (irreversible)
# delete the alien hook line from your rc file
```

## License

[MIT](LICENSE) — see LICENSE.

## Credits

- [fzf](https://github.com/junegunn/fzf) — the fuzzy finder that powers
  the picker
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) and
  [Lip Gloss](https://github.com/charmbracelet/lipgloss) — the TUI
  framework behind the pack browser
- [VHS](https://github.com/charmbracelet/vhs) — the recorder used to
  generate the demo above
