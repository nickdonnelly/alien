#!/usr/bin/env bash
# alien installer
#
# Builds the `alien` binary, installs it to a directory on $PATH, and (with
# your confirmation) wires up the shell hook in your rc file.

set -euo pipefail

PREFIX="${PREFIX:-$HOME/.local}"
BIN="$PREFIX/bin"

cyan()  { printf '\033[36m%s\033[0m' "$*"; }
green() { printf '\033[32m%s\033[0m' "$*"; }
red()   { printf '\033[31m%s\033[0m' "$*"; }
dim()   { printf '\033[2m%s\033[0m' "$*"; }

step() { printf '%s %s\n' "$(cyan '››')" "$*"; }
ok()   { printf '%s %s\n' "$(green '✓')" "$*"; }
fail() { printf '%s %s\n' "$(red '✗')" "$*" >&2; exit 1; }

cd "$(dirname "$0")"

command -v go  >/dev/null 2>&1 || fail "go is required (install from https://go.dev/dl/)"
command -v fzf >/dev/null 2>&1 || printf '%s fzf is recommended (https://github.com/junegunn/fzf)\n' "$(dim '!')"

step "Building alien"
go build -trimpath -ldflags="-s -w" -o alien .
ok "built ./alien"

step "Installing to $BIN"
mkdir -p "$BIN"
install -m 0755 alien "$BIN/alien"
ok "installed $BIN/alien"

case ":$PATH:" in
  *":$BIN:"*) ;;
  *) printf '%s %s is not on your $PATH. Add this to your shell rc:\n  export PATH="%s:$PATH"\n' \
       "$(dim '!')" "$BIN" "$BIN" ;;
esac

shell_name="$(basename "${SHELL:-/bin/bash}")"
case "$shell_name" in
  zsh)  rc="$HOME/.zshrc";   hook='source <(alien init zsh)'  ;;
  bash) rc="$HOME/.bashrc";  hook='eval "$(alien init bash)"' ;;
  *)    printf '%s unknown shell %q — add the alien hook to your rc manually.\n' \
          "$(dim '!')" "$shell_name"; exit 0 ;;
esac

if [[ -f "$rc" ]] && grep -q "alien init" "$rc"; then
  ok "shell hook already present in $rc"
  exit 0
fi

printf '\nAdd the alien hook to %s? [Y/n] ' "$rc"
read -r reply
reply="${reply:-y}"
if [[ "$reply" =~ ^[Yy] ]]; then
  printf '\n# alien :: %s\n%s\n' "$(date +%Y-%m-%d)" "$hook" >> "$rc"
  ok "appended hook to $rc"
  printf '\n%s open a new shell or run %s to start using alien.\n' \
    "$(green '✓')" "$(cyan "source $rc")"
else
  printf '\nAdd this line to your rc when ready:\n  %s\n' "$hook"
fi
