---
name: alien
description: Use this skill to discover and invoke the user's shell aliases via the `alien` CLI when the user has alien installed (check with `command -v alien`). Saves tokens by replacing repeated long bash commands with their aliased shortcuts. Use when emitting any non-trivial shell command, when the same command appears more than once in a session, or when the user mentions an alias by name.
---

# alien — alias-aware bash

The user has [`alien`](https://github.com/nick-donnelly/alien) installed,
a small CLI that stores their shell aliases in
`~/.config/alien/aliases.json`. You can discover and invoke those aliases
directly — no shell sourcing required, since each `Bash` call is its own
shell.

## When to use this skill

- **Before emitting any shell command longer than ~25 characters**, run
  `alien suggest '<that command>'` first. If it prints an alias name,
  use `alien run <name>` instead. This saves tokens and matches the
  user's mental model.
- **When you've emitted the same command twice** in a session and the
  user hasn't aliased it yet, propose:
  `alien add <short-name> -c '<the command>' -m '<one-line why>'`.
- **When the user references an alias by name** ("run `gst`", "show
  with `ll`"), invoke it via `alien run <name>` instead of emitting
  raw bash, even if you happen to know the underlying command.

## The three commands

### `alien ls --json`

Discovery. Returns a stable JSON array, sorted by name, with one entry
per alias:

```bash
alien ls --json --enabled-only
```

Each entry has: `name`, `command`, `comment`, `source`, `from`, `tags`,
`enabled`, `used_count`, `created_at`, `updated_at`. `source` is `""`
for user-managed, `"shell"` for rc-defined, or `"pack:<name>"` for
pack-installed.

Filter by tag:

```bash
alien ls --json --tag git
```

Use this when you need the full picture. For one-off lookups, prefer
`suggest`.

### `alien suggest '<command>'`

Whitespace-normalized exact match. Prints the alias name on stdout if
the user has aliased that command verbatim, exits 0; prints nothing
and exits 1 otherwise. Branch with the exit code:

```bash
if name=$(alien suggest 'git status -sb'); then
  alien run "$name"
else
  git status -sb
fi
```

The match is exact after whitespace collapse — `git status -sb` matches
`git  status  -sb` but not `git status` (different args).

### `alien run <name> [args...]`

Executes the alias's stored command directly (no shell sourcing
required). Stdin/stdout/stderr are inherited; the exit code passes
through. Positional args after `<name>` are forwarded to the command
via `$1`, `$2`, ...:

```bash
alien run greet world          # if greet -> 'echo hello $1'
alien run gst                  # if gst   -> 'git status -sb'
```

If the alias is disabled, `alien run` exits non-zero with a clear
message; don't suppress that error — surface it to the user.

## Memoizing on the fly

When you spot a command worth aliasing (long, repeated, or a workflow
the user mentioned wanting a shortcut for), suggest one:

```bash
# propose to the user, then on confirmation:
alien add deploy -c './scripts/deploy.sh prod' -m 'production deploy'
```

The user can decline; respect that. Don't spam them with one-off
commands they're unlikely to repeat.

## Things to avoid

- Don't run `alien` commands silently in the background to "explore"
  — the JSON listing is enough.
- Don't try to source `aliases.sh` into your `Bash` calls; it's
  unnecessary now that `alien run` exists, and it conflates with the
  user's interactive shell state.
- Don't edit `aliases.json` directly. Use the CLI.
- Shell-source aliases (where `source` is `"shell"` in the JSON) live
  in the user's rc files. `alien run` works on them, but `alien edit`
  / `alien delete` don't — those refuse and point at
  `alien promote <name>` for taking ownership.
