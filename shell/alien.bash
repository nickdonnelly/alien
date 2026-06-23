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
: "${ALIEN_KEYBIND:=\C-g}" # Ctrl-G; set to "" to skip binding
: "${ALIEN_FZF_OPTS:=}"
# Tab-insert mode (alias "name" vs expanded "command") is read from
# `alien config` (set it with: alien config set tab-insert command). Export
# ALIEN_TAB_INSERT to override the stored setting for this shell only.
: "${ALIEN_TRACK:=1}" # set to 0 to disable usage tracking

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
  ALIEN_RC_FILES="${_alien_rc%:}" alias 2>/dev/null |
    ALIEN_RC_FILES="${_alien_rc%:}" command alien import-shell --quiet
  unset _alien_rc _f
  command alien sync maybe-pull 2>/dev/null
fi

# ---------------- usage tracking -----------------------------------------
# Counts real alias invocations. bash 3.2 (macOS default) has no preexec,
# PS0, or associative arrays, so this reads `history 1` from PROMPT_COMMAND
# (one fork per prompt — the unavoidable cost on old bash; set ALIEN_TRACK=0
# to opt out) and matches the first word against a space-delimited name list
# loaded from names.txt (no fork). Hits append to hits.log; `alien track
# flush` folds them into usage.json on startup and after alien invocations.
#
# Known accuracy limits (fine for frequency telemetry): misses `sudo ll`,
# `VAR=1 ll`, aliases mid-pipeline, and HISTIGNOREd commands.
_alien_name_list=""
_alien_load_names() {
  _alien_name_list=""
  [[ -r "$ALIEN_HOME/names.txt" ]] || return 0
  local _n
  while IFS= read -r _n; do
    [[ -n $_n ]] && _alien_name_list+=" $_n"
  done <"$ALIEN_HOME/names.txt"
  [[ -n $_alien_name_list ]] && _alien_name_list+=" "
  return 0
}

_alien_track_prompt() {
  [[ -n "$_alien_name_list" ]] || return 0
  local raw last num w
  raw=$(HISTTIMEFORMAT='' history 1) || return 0
  # First prompt of the session: history holds the previous session's last
  # command — record the baseline, don't count it.
  if [[ -z "${_alien_last_seen+x}" ]]; then
    _alien_last_seen="$raw"
    return 0
  fi
  # The raw line includes the history number, so back-to-back identical
  # commands still count separately while empty prompts (history unchanged)
  # are skipped.
  [[ "$raw" == "$_alien_last_seen" ]] && return 0
  _alien_last_seen="$raw"
  # Strip "  123* " (number, optional modified-entry star) to get the command.
  last="${raw#"${raw%%[![:space:]]*}"}"
  num="${last%%[![:digit:]]*}"
  last="${last#"$num"}"
  last="${last#\*}"
  last="${last#"${last%%[![:space:]]*}"}"
  w="${last%%[[:space:]]*}"
  case "$_alien_name_list" in
  *" $w "*) printf '%s\n' "$w" >>"$ALIEN_HOME/hits.log" ;;
  esac
  return 0
}

if [[ "$ALIEN_TRACK" != 0 && $- == *i* ]] && command -v alien >/dev/null 2>&1; then
  _alien_load_names
  unset _alien_last_seen
  PROMPT_COMMAND="_alien_track_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
  # Fold hits from previous sessions into usage.json.
  command alien track flush 2>/dev/null
fi

# `alien` wrapper: capture previous command and pass it via --prev-cmd, then
# re-source aliases so changes take effect immediately.
alien() {
  if (($# == 0)); then
    command alien
    return
  fi
  # `chain` needs more than the previous line — pipe a window of recent
  # history (oldest first) into the binary so the TUI can pick from it.
  if [[ "$1" == "chain" ]]; then
    local rc=0
    # Both atuin and fc emit oldest-first; the binary reverses internally
    # to display newest-at-top. Just pipe raw.
    if command -v atuin >/dev/null 2>&1; then
      atuin history list --cmd-only --session 2>/dev/null |
        tail -200 | command alien "$@"
    else
      HISTTIMEFORMAT='' fc -ln -50 2>/dev/null | command alien "$@"
    fi
    rc=$?
    [[ -r "$ALIEN_HOME/aliases.sh" ]] && source "$ALIEN_HOME/aliases.sh"
    return $rc
  fi
  local prev
  # `fc -ln -1` works in bash too. The leading whitespace is variable across
  # bash versions so strip it.
  prev=$(HISTTIMEFORMAT='' fc -ln -1 2>/dev/null)
  prev="${prev#"${prev%%[![:space:]]*}"}"
  command alien --prev-cmd "$prev" "$@"
  local rc=$?
  [[ -r "$ALIEN_HOME/aliases.sh" ]] && source "$ALIEN_HOME/aliases.sh"
  # Refresh the tracking name set (the alias list may have just changed)
  # and fold any pending hits, detached so it never blocks the prompt.
  if [[ "$ALIEN_TRACK" != 0 ]]; then
    _alien_load_names
    (command alien track flush 2>/dev/null &) 2>/dev/null
  fi
  (command alien sync maybe-push 2>/dev/null &) 2>/dev/null
  return $rc
}

# `a` shortcut: bare invocation opens the picker; otherwise delegates.
a() {
  if (($# == 0)); then
    _alien_pick_cli
  else
    alien "$@"
  fi
}

# ---------------- fuzzy picker -----------------------------------------

_alien_run_fzf() {
  command alien tab set all >/dev/null 2>&1

  command alien fzf --filter all 2>/dev/null | fzf \
    --ansi \
    --delimiter=$'\t' \
    --with-nth=2 \
    --header-lines=3 \
    --no-multi \
    --reverse \
    --height=50% \
    --info=inline \
    --pointer='›' \
    --marker='✓' \
    --prompt='👽 alien › ' \
    --color='border:bright-cyan,prompt:bright-cyan,pointer:bright-magenta,header:gray,info:gray,hl:bright-yellow,hl+:bright-yellow' \
    --expect='tab,ctrl-e,ctrl-d' \
    --bind '[:transform(alien tab prev)' \
    --bind ']:transform(alien tab next)' \
    $ALIEN_FZF_OPTS
}

# _alien_tab_text echoes what Tab should insert for the selected alias: the
# alias name (default) or its expanded command. The mode comes from the
# ALIEN_TAB_INSERT override if set, else `alien config get tab-insert`. On any
# lookup miss it falls back to the name so Tab is never a no-op.
_alien_tab_text() {
  local name=$1 cmd mode
  mode=${ALIEN_TAB_INSERT:-$(command alien config get tab-insert 2>/dev/null)}
  case "$mode" in
  command | cmd | expanded)
    cmd=$(command alien get "$name" 2>/dev/null)
    if [[ -n "$cmd" ]]; then
      printf '%s' "$cmd"
      return
    fi
    ;;
  esac
  printf '%s' "$name"
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
    # command here. Record the *alias name* in history (not the expanded
    # command) so ↑-arrow behaves the way it would if you'd typed the
    # alias yourself.
    cmd=$(command alien get "$name") || return 0
    printf '\n\033[2m» %s\033[0m\n' "$name"
    history -s -- "$name"
    eval -- "$cmd"
    READLINE_LINE=
    READLINE_POINT=0
    ;;
  tab)
    READLINE_LINE+="$(_alien_tab_text "$name") "
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
    history -s -- "$name"
    eval -- "$cmd"
    ;;
  tab)
    printf '%s\n' "$(_alien_tab_text "$name")"
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
