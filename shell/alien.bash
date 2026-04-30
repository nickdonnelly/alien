# alien :: bash integration
# Wire this into your .bashrc:
#   eval "$(alien init bash)"
#
# (`source <(alien init bash)` works on bash 4+, but loses function definitions
# on the macOS-default bash 3.2 due to a process-substitution bug. `eval`
# works everywhere.)
#
# Provides:
#   * `alien <name>` and `a <name>` to add an alias from your last command
#   * Ctrl-G to open a fuzzy picker over your aliases
#   * automatic re-sourcing of aliases after every change

# --- config (override before sourcing) ----------------------------------
: "${ALIEN_HOME:=${XDG_CONFIG_HOME:-$HOME/.config}/alien}"
: "${ALIEN_KEYBIND:=\C-g}"          # Ctrl-G; set to "" to skip binding
: "${ALIEN_FZF_OPTS:=}"

# Source active aliases on startup. The Go binary regenerates this file after
# every modification so it's always cheap and always current.
[[ -r "$ALIEN_HOME/aliases.sh" ]] && source "$ALIEN_HOME/aliases.sh"

# Index aliases already defined in rc files and plugins so the picker can
# search them too. Pipe `alias` to the binary which merges as Source=shell.
# Pass our rc file list so the binary can fill the FROM column with a real
# origin label (".bashrc", ".bash_aliases", ...) instead of a generic "shell".
if command -v alien >/dev/null 2>&1; then
  _alien_rc=""
  for _f in ~/.bashrc ~/.bash_profile ~/.profile ~/.aliases ~/.bash_aliases; do
    [[ -r $_f ]] && _alien_rc+="$_f:"
  done
  ALIEN_RC_FILES="${_alien_rc%:}" alias 2>/dev/null | \
    ALIEN_RC_FILES="${_alien_rc%:}" command alien import-shell --quiet
  unset _alien_rc _f
  command alien sync maybe-pull 2>/dev/null
fi

# `alien` wrapper: capture previous command and pass it via --prev-cmd, then
# re-source aliases so changes take effect immediately.
alien() {
  if (( $# == 0 )); then
    command alien
    return
  fi
  local prev
  # `fc -ln -1` works in bash too. The leading whitespace is variable across
  # bash versions so strip it.
  prev=$(HISTTIMEFORMAT='' fc -ln -1 2>/dev/null)
  prev="${prev#"${prev%%[![:space:]]*}"}"
  command alien --prev-cmd "$prev" "$@"
  local rc=$?
  [[ -r "$ALIEN_HOME/aliases.sh" ]] && source "$ALIEN_HOME/aliases.sh"
  (command alien sync maybe-push 2>/dev/null &) 2>/dev/null
  return $rc
}

# `a` shortcut: bare invocation opens the picker; otherwise delegates.
a() {
  if (( $# == 0 )); then
    _alien_pick_cli
  else
    alien "$@"
  fi
}

# ---------------- fuzzy picker -----------------------------------------

_alien_run_fzf() {
  command alien tab set all >/dev/null 2>&1
  local _alien_header
  _alien_header=$(command alien fzf-header --filter all)

  command alien fzf --filter all 2>/dev/null | fzf \
    --ansi \
    --delimiter=$'\t' \
    --with-nth=2 \
    --no-multi \
    --reverse \
    --height=50% \
    --info=inline \
    --pointer='›' \
    --marker='✓' \
    --prompt='👽 alien › ' \
    --header="$_alien_header" \
    --color='border:bright-cyan,prompt:bright-cyan,pointer:bright-magenta,header:gray,info:gray,hl:bright-yellow,hl+:bright-yellow' \
    --expect='tab,ctrl-e,ctrl-d' \
    --bind '[:transform(alien tab prev)' \
    --bind ']:transform(alien tab next)' \
    $ALIEN_FZF_OPTS
}

# Readline binding (`bind -x`) target. Bash exposes the prompt buffer as
# READLINE_LINE / READLINE_POINT; we manipulate those for the insert/run cases.
_alien_fzf_widget() {
  local result key line name cmd
  result=$(_alien_run_fzf </dev/tty)
  key=$(printf '%s\n' "$result" | sed -n '1p')
  line=$(printf '%s\n' "$result" | sed -n '2p')
  name=${line%%$'\t'*}
  [[ -z "$name" ]] && return 0

  case "$key" in
    "")
      # Enter: execute the alias's command directly. Bash doesn't have a
      # clean "accept this line" hook from inside `bind -x`, so we run the
      # command here and add it to history for ↑-arrow recall.
      cmd=$(command alien get "$name") || return 0
      printf '\n\033[2m» %s\033[0m\n' "$name"
      history -s -- "$cmd"
      eval -- "$cmd"
      READLINE_LINE=
      READLINE_POINT=0
      ;;
    tab)
      READLINE_LINE+="$name "
      READLINE_POINT=${#READLINE_LINE}
      ;;
    ctrl-e)
      command alien edit "$name"
      ;;
    ctrl-d)
      command alien delete "$name"
      ;;
  esac
}

# Non-readline picker for `a` with no args.
_alien_pick_cli() {
  if ! command -v fzf >/dev/null 2>&1; then
    printf 'alien: fzf is required for the picker\n' >&2
    return 1
  fi
  local result key line name cmd
  result=$(_alien_run_fzf)
  key=$(printf '%s\n' "$result" | sed -n '1p')
  line=$(printf '%s\n' "$result" | sed -n '2p')
  name=${line%%$'\t'*}
  [[ -z "$name" ]] && return 0
  case "$key" in
    "")
      cmd=$(command alien get "$name") || return 0
      printf '\033[2m» %s\033[0m\n' "$name"
      history -s -- "$cmd"
      eval -- "$cmd"
      ;;
    tab)
      printf '%s\n' "$name"
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
  # Only bind if running interactively; non-interactive bash has no readline.
  if [[ $- == *i* ]]; then
    bind -x "\"$ALIEN_KEYBIND\": _alien_fzf_widget" 2>/dev/null
  fi
fi
