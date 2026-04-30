# alien :: zsh integration
# Source this from your .zshrc:
#   source <(alien init zsh)
#
# Provides:
#   * `alien <name>` and `a <name>` to add an alias from your last command
#   * Ctrl-G to open a fuzzy picker over your aliases
#   * automatic re-sourcing of aliases after every change

# --- config (override before sourcing) ----------------------------------
: ${ALIEN_HOME:="${XDG_CONFIG_HOME:-$HOME/.config}/alien"}
: ${ALIEN_KEYBIND:="^G"}            # Ctrl-G; set to "" to skip binding
: ${ALIEN_FZF_OPTS:=""}             # extra flags appended to fzf

# Source the active aliases on startup. The Go binary regenerates this file
# after every modification so it's always cheap and always current.
[[ -r "$ALIEN_HOME/aliases.sh" ]] && source "$ALIEN_HOME/aliases.sh"

# Index aliases the user already has defined in their rc files and plugins.
# We pipe `alias` (the shell builtin) into the binary, which merges them into
# the store with Source=shell so the picker can search them too. Stale rc
# entries get pruned automatically.
if command -v alien >/dev/null 2>&1; then
  alias 2>/dev/null | command alien import-shell --quiet
  # Optional auto-pull (no-op unless enabled and throttle elapsed).
  command alien sync maybe-pull 2>/dev/null
fi

# `alien` shell function: captures the previous command via `fc -ln -1` so that
# `alien <name>` (with no explicit --cmd) can use it. Then re-sources the
# aliases file so the new alias is live immediately in this shell.
alien() {
  emulate -L zsh
  if (( $# == 0 )); then
    command alien
    return
  fi
  local prev
  prev=$(fc -ln -1 2>/dev/null)
  prev=${prev##[[:space:]]##}
  command alien --prev-cmd "$prev" "$@"
  local rc=$?
  [[ -r "$ALIEN_HOME/aliases.sh" ]] && source "$ALIEN_HOME/aliases.sh"
  # Background auto-push if enabled. Detached so a slow remote never blocks
  # the user's prompt.
  (command alien sync maybe-push 2>/dev/null &) 2>/dev/null
  return $rc
}

# `a` is the convenience shortcut. We use a function (not an alias) so it can
# accept arguments cleanly and so it can also launch the picker when called
# bare from a non-ZLE context.
a() {
  if (( $# == 0 )); then
    _alien_pick_cli
  else
    alien "$@"
  fi
}

# ---------------- fuzzy picker (ZLE widget) -----------------------------
#
# Bound to $ALIEN_KEYBIND (default Ctrl-G). Reads tab-delimited rows from
# `alien fzf` (col1 = name, col2 = pretty display) and dispatches based on
# which key the user pressed inside fzf:
#   enter -> run the alias's command in the current shell
#   tab   -> insert the alias name into the command line
#   e     -> open `alien edit <name>`
#   d     -> open `alien delete <name>`

_alien_run_fzf() {
  command alien fzf 2>/dev/null | fzf \
    --ansi \
    --delimiter=$'\t' \
    --with-nth=2 \
    --no-multi \
    --reverse \
    --height=40% \
    --info=inline \
    --pointer='›' \
    --marker='✓' \
    --prompt='👽 alien › ' \
    --header='enter:run · tab:insert · ctrl-e:edit · ctrl-d:delete · esc:cancel' \
    --color='border:bright-cyan,prompt:bright-cyan,pointer:bright-magenta,header:gray,info:gray,hl:bright-yellow,hl+:bright-yellow' \
    --expect='tab,ctrl-e,ctrl-d' \
    ${=ALIEN_FZF_OPTS}
}

_alien_fzf_widget() {
  emulate -L zsh
  setopt local_options pipe_fail no_aliases

  local result key line name cmd
  result=$(_alien_run_fzf </dev/tty)
  local -a lines=("${(@f)result}")
  key=${lines[1]}
  line=${lines[2]}
  name=${line%%$'\t'*}

  if [[ -z "$name" ]]; then
    zle reset-prompt
    return 0
  fi

  case "$key" in
    "")
      cmd=$(command alien get "$name") || { zle reset-prompt; return 0; }
      BUFFER="$cmd"
      CURSOR=${#BUFFER}
      zle accept-line
      ;;
    tab)
      LBUFFER+="$name "
      zle redisplay
      ;;
    ctrl-e)
      zle -I
      command alien edit "$name"
      zle reset-prompt
      ;;
    ctrl-d)
      zle -I
      command alien delete "$name"
      zle reset-prompt
      ;;
  esac
}
zle -N _alien_fzf_widget

# Non-ZLE picker, used by `a` with no args. Same behavior, but we can't write
# into the prompt buffer from here, so for the "tab" case we just print the
# alias name. For "enter" we run the command directly in the current shell.
_alien_pick_cli() {
  emulate -L zsh
  if ! command -v fzf >/dev/null 2>&1; then
    print -u2 "alien: fzf is required for the picker"
    return 1
  fi
  local result key line name cmd
  result=$(_alien_run_fzf)
  local -a lines=("${(@f)result}")
  key=${lines[1]}
  line=${lines[2]}
  name=${line%%$'\t'*}
  [[ -z "$name" ]] && return 0
  case "$key" in
    "")
      cmd=$(command alien get "$name") || return 0
      print -P "%F{8}» $name%f"
      print -s -- "$cmd"
      eval -- "$cmd"
      ;;
    tab)
      print -- "$name"
      ;;
    ctrl-e)
      command alien edit "$name"
      ;;
    ctrl-d)
      command alien delete "$name"
      ;;
  esac
}

if [[ -n "$ALIEN_KEYBIND" ]]; then
  bindkey "$ALIEN_KEYBIND" _alien_fzf_widget
fi

# ---------------- completion -------------------------------------------
# When the user presses Tab on `a <TAB>` or `alien <TAB>`, jump straight into
# the fuzzy picker. They can still type subcommand names manually.
_alien_complete_widget() {
  local words=(${(z)BUFFER})
  if (( ${#words} <= 2 )) && [[ "${words[1]}" == (a|alien) ]]; then
    _alien_fzf_widget
  else
    zle expand-or-complete
  fi
}
zle -N _alien_complete_widget
# Disabled by default — uncomment to override Tab on `a`:
#bindkey '^I' _alien_complete_widget
