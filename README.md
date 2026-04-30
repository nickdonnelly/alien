# 👽 alien

Quick command-line aliases. Run a command, like it, type `alien <name>`, done.

```
$ ls -al
$ alien ll
✓ aliased ll → ls -al

$ ll
total 24
drwxr-xr-x  9 you  staff  288 Apr 29 10:12 .
...
```

The new alias is active **immediately in the current shell** — no `exec zsh`,
no manual sourcing.

## Install

```bash
./install.sh
```

This builds the binary, installs it to `~/.local/bin/alien`, and (with your
confirmation) appends the shell hook to your `~/.zshrc` or `~/.bashrc`.
Open a new shell afterwards.

If you'd rather wire it up by hand:

```bash
go build -o alien .
mv alien ~/.local/bin/

# then, in your rc file:
source <(alien init zsh)        # zsh
eval "$(alien init bash)"       # bash (works on bash 3.2 too)
```

Requires Go (to build) and [fzf](https://github.com/junegunn/fzf) (for the
fuzzy picker).

## How you use it

```text
alien <name>                add an alias for the command you just ran
alien <name> -c "cmd"       add an explicit alias
alien <name> -m "comment"   describe what it does
a <name>                    same as `alien <name>` (short form)

alien list                  pretty list of all aliases
alien show <name>           details for one alias
alien edit <name>           open in $EDITOR (rename, retarget, toggle)
alien comment <name> ...    set or clear the comment
alien toggle <name>         enable/disable
alien delete <name>         remove (with confirmation)
alien promote <name>        take ownership of a shell-imported alias
```

### Sources: user · shell · pack

Every alias has a *source*. The picker badges them so you always know what
you're looking at:

```text
● gst       git status              # quick status
● dps       docker ps               [pack:docker]  # running containers
● ll        ls -lah                 [shell]        # from your .zshrc
○ deploy    ./scripts/deploy.sh                    # disabled
```

- **user** (no badge): you added it via `alien <name>`.
- **shell**: defined in your rc / plugins. alien indexes them on shell
  startup so the picker is one search surface for *all* your aliases.
  alien refuses to delete or edit them — they live in your rc, not here.
  Run `alien promote <name>` to take ownership.
- **pack:`<name>`**: installed by `alien ufo install <name>`. Cleanly
  removed by `alien ufo uninstall <name>`.

Type `shell` or `pack:docker` into the picker to scope your search.

## UFO packs

A *pack* (or *ufo*, on theme) is a bundle of topical aliases — docker
shortcuts, common git aliases, file utilities — that you can install in one
go and uninstall just as cleanly. alien ships with a few starter packs
embedded in the binary; you can also build and share your own.

```text
alien ufo list                        # installed + built-in available
alien ufo show docker                 # preview pack contents
alien ufo install docker              # opens an interactive TUI browser
alien ufo install docker -y           # non-interactive, auto-rename conflicts
alien ufo install ./mypack.ufo.json   # install from a file
alien ufo install https://…/x.ufo.json # install from a URL
alien ufo uninstall docker            # remove only the entries this pack added
alien ufo create mypack > mypack.ufo.json   # publish your own user aliases
alien ufo export docker               # dump an installed pack back to JSON
```

The interactive install browser (Bubble Tea) lets you select/deselect
individual aliases, sees and warns about conflicts (with your aliases,
shell aliases, or other installed packs), and lets you press `r` to rename
on the spot.

```
┌──────────────────────────────────────────────────────────────┐
│ 👽 alien ufo › install: docker v1.0.0                        │
│ Common Docker and Compose shortcuts                          │
├──────────────┬───────────────────────────────────────────────┤
│ › [x] dps    │ command : docker ps                           │
│   [x] dpsa   │ comment : list running containers             │
│   [ ] di  ⚠  │ status  : conflicts with your alias           │
│   [x] dlog   │ install : di-docker (renamed from di)         │
├──────────────┴───────────────────────────────────────────────┤
│ space:toggle  a:all  n:none  r:rename  enter:install  q:quit │
└──────────────────────────────────────────────────────────────┘
```

### Pack file format (`.ufo.json`)

```json
{
  "ufo": {
    "name": "docker",
    "version": "1.0.0",
    "description": "Common Docker shortcuts",
    "author": "you"
  },
  "aliases": {
    "dps":  { "command": "docker ps",     "comment": "running containers" },
    "dpsa": { "command": "docker ps -a",  "comment": "all containers" }
  }
}
```

Built-in starter packs: **docker**, **git**, **files**, **nav**.

## Sync

Opt-in. `alien sync` makes your `$ALIEN_HOME` directory a git working tree,
so you get version control *and* cross-machine sync from the same feature.

```text
alien sync init <repo-url>     # set up; pulls if remote has content,
                               # otherwise pushes the local state
alien sync push [-m "msg"]     # commit + push current state
alien sync pull                # pull --rebase, surface conflicts
alien sync status              # short status
alien sync auto on             # auto-pull on shell startup, auto-push after changes
alien sync auto off            # turn that off
alien sync auto on push        # only one direction
alien sync forget              # disconnect (rm .git, sync.json)
```

Only `aliases.json` is tracked — generated `aliases.sh`, sync state, and
backup files are gitignored.

When auto-sync is on, the shell hook runs a throttled `sync maybe-pull`
(default: at most once every 5 minutes per shell) and a backgrounded
`sync maybe-push` after each `alien` modification. Both are no-ops if
auto-sync is off, so leaving them in the hook costs nothing.

Conflict path: if `aliases.json` diverged across machines, `alien sync pull`
surfaces git's standard conflict markers in `aliases.json`. Resolve them
in your editor, then `git -C $ALIEN_HOME rebase --continue` and
`alien sync push`.

### The fuzzy picker

Press **Ctrl-G** in your shell to open the picker:

```text
👽 alien › ll                                      enter:run · tab:insert · ctrl-e:edit · ctrl-d:delete · esc:cancel
  › ● ll      ls -al                # long listing
    ● gst     git status            # quick status
    ○ deploy  ./scripts/deploy.sh   # disabled
```

- **enter** runs the alias's command in your current shell
- **tab** inserts the alias name into your prompt (no execute yet)
- **ctrl-e** opens the alias for editing in `$EDITOR`
- **ctrl-d** prompts to delete it
- type to fuzzy-search across name, command, and comment

> Why ctrl-e/ctrl-d instead of plain `e`/`d`? fzf reads letter keys as
> filter input — using them as actions would break fuzzy search.

You can also type `a` with no arguments to open the picker.

## Configuration

Set these before sourcing the hook (e.g. in `~/.zshrc`):

| Variable          | Default                    | Purpose                                |
|-------------------|----------------------------|----------------------------------------|
| `ALIEN_HOME`      | `$XDG_CONFIG_HOME/alien`   | where aliases.json lives               |
| `ALIEN_KEYBIND`   | `^G` (zsh) / `\C-g` (bash) | picker keybinding; empty to disable    |
| `ALIEN_FZF_OPTS`  | (empty)                    | extra flags forwarded to fzf           |

## Storage

Aliases are kept in `$ALIEN_HOME/aliases.json` (default
`~/.config/alien/aliases.json`). The binary regenerates `aliases.sh` from
that on every change; the shell hook sources that file so new aliases
activate instantly. Shell-source entries (already defined in your rc) are
indexed for search but *not* re-emitted into `aliases.sh`.

Files in `$ALIEN_HOME`:

| File             | Purpose                                                         |
|------------------|-----------------------------------------------------------------|
| `aliases.json`   | source of truth (user + pack + shell entries, atomic writes)    |
| `aliases.sh`     | generated cache; sourced by your shell hook                     |
| `sync.json`      | sync remote URL + auto-sync flags (gitignored)                  |
| `.last_pull`     | throttle marker for auto-pull (gitignored)                      |
| `.git/`          | only present after `alien sync init`                            |

## Why a Go binary plus shell scripts?

- **Speed.** Alias storage operations are a few milliseconds.
- **Single binary.** No interpreter, no runtime deps beyond fzf.
- **Shell-native where it has to be.** Capturing `fc -ln -1`, sourcing
  aliases into the *current* shell, and binding ZLE / readline widgets
  cannot be done from a subprocess — those parts live in the shell hook.

## Uninstall

```bash
make uninstall                  # remove the binary
rm -rf ~/.config/alien          # remove your aliases (irreversible)
# delete the `source <(alien init …)` line from your rc file
```
