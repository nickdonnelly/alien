package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tab state lives in $ALIEN_HOME/.tab as a single line containing the active
// tab name (e.g. "all", "user", "shell", "pack:docker"). The shell hook
// initializes it lazily before launching the picker; bracket-key presses
// inside fzf cycle it via `alien tab next/prev`, which emits a sequence of
// fzf actions for the picker to apply.

func tabStatePath() string { return filepath.Join(dataDir(), ".tab") }

func readTab() string {
	data, err := os.ReadFile(tabStatePath())
	if err != nil {
		return "all"
	}
	t := strings.TrimSpace(string(data))
	if t == "" {
		return "all"
	}
	return t
}

func writeTab(t string) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(tabStatePath(), []byte(t+"\n"), 0o644)
}

// cmdTab handles `alien tab <action>`. Most actions are wired through the
// fzf widget's bracket bindings; a few (`get`, `set`, `list`) are useful
// for shell scripts and debugging.
func cmdTab(args []string) {
	if len(args) == 0 {
		fmt.Println(readTab())
		return
	}
	switch args[0] {
	case "get":
		fmt.Println(readTab())
	case "list":
		s, _ := loadStore()
		for _, t := range availableTabs(s) {
			fmt.Println(t)
		}
	case "set":
		if len(args) < 2 {
			errorf("usage: alien tab set <name>")
			os.Exit(1)
		}
		_ = writeTab(args[1])
	case "next":
		fmt.Print(cycleAndEmit(+1))
	case "prev":
		fmt.Print(cycleAndEmit(-1))
	case "reset":
		_ = writeTab("all")
		fmt.Print(emitFzfActions("all"))
	default:
		errorf("unknown subcommand: alien tab %s", args[0])
		os.Exit(1)
	}
}

// cycleAndEmit advances the active tab by `step` (+1 / -1) and returns the
// fzf transform output that retargets the picker to the new tab.
func cycleAndEmit(step int) string {
	s, _ := loadStore()
	tabs := availableTabs(s)
	cur := readTab()
	idx := 0
	for i, t := range tabs {
		if t == cur {
			idx = i
			break
		}
	}
	idx = (idx + step + len(tabs)) % len(tabs)
	next := tabs[idx]
	_ = writeTab(next)
	return emitFzfActions(next)
}

// emitFzfActions retargets the picker to `tab`: reload the data, swap the
// prompt, and clear the previous query so the user sees all entries in the
// new scope. The picker's header is emitted as the first three lines of
// `alien fzf` output (consumed by --header-lines=3), so a reload alone
// refreshes the header — no `change-header` action needed, which sidesteps
// fzf's lack of \n interpretation inside transform output.
func emitFzfActions(tab string) string {
	prompt := "👽 alien"
	if tab != "all" {
		prompt = "👽 " + tabLabel(tab)
	}
	prompt += " › "

	var b strings.Builder
	fmt.Fprintf(&b, "reload(alien fzf --filter %s)", tab)
	fmt.Fprintf(&b, "+change-prompt(%s)", prompt)
	b.WriteString("+clear-query\n")
	return b.String()
}
