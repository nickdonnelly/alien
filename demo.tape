# alien — VHS demo
# Run with: vhs demo.tape

Output demo.mp4

Require alien
Require fzf

Set Shell zsh
Set FontSize 16
Set Width 1280
Set Height 720
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 50ms

# ---------- silent setup ----------
# Everything inside Hide is invisible to VHS. We bundle setup into a single
# line, redirect every byte to /dev/null, sleep so zsh fully returns to its
# prompt, then send Ctrl-L to clear the screen state before recording.
Hide
Type `export PATH=/Users/molybdenum/projects/alien:$PATH; export ALIEN_HOME=$(mktemp -d); alias ll='ls -alh'; alias gst='git status -sb'; alias gco='git checkout'; source <(alien init zsh) >/dev/null 2>&1; clear`
Enter
Sleep 2s
Ctrl+L
Sleep 600ms
Show

# ---------- scene 1: turn the last command into an alias ----------
Type "echo 'I just ran a command and want a shortcut for it…'"
Enter
Sleep 1500ms

Type "ls -alh ~/.config 2>/dev/null | head -6"
Enter
Sleep 2500ms

Type "alien lc -m 'list ~/.config'"
Enter
Sleep 2s

Type "lc | head -3"
Enter
Sleep 2500ms

# ---------- scene 2: install a pack ----------
Ctrl+L
Sleep 500ms

Type "alien ufo install git -y"
Enter
Sleep 2s

Type "alien ufo list"
Enter
Sleep 3500ms

# ---------- scene 3: the picker ----------
Ctrl+L
Sleep 500ms

Type "a"
Enter
Sleep 1500ms

Type "]"
Sleep 1000ms
Type "]"
Sleep 1000ms
Type "]"
Sleep 1500ms

Type "log"
Sleep 1500ms

Escape
Sleep 800ms

# ---------- scene 4: list with badges ----------
Type "alien list | head -20"
Enter
Sleep 4s
