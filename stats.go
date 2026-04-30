package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"
)

type statRow struct {
	Name      string
	Command   string
	Source    string
	UsedCount int
	UpdatedAt time.Time
	CreatedAt time.Time
	Enabled   bool
}

// cmdStats summarises usage. UsedCount is incremented by `alien run`;
// aliases triggered through the picker route through `alien run` too,
// but commands typed at the prompt and run via the shell's own alias
// expansion don't — so the absolute numbers are a lower bound. The
// pretty output surfaces this with a "tracked invocations" label.
func cmdStats(args []string) {
	args, wantJSON := extractBoolFlag(args, "--json")
	_, topStr := extractFlag(args, "--top")
	top := 10
	if topStr != "" {
		if n, err := strconv.Atoi(topStr); err == nil && n > 0 {
			top = n
		}
	}

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}

	rows := make([]statRow, 0, len(s.Aliases))
	for n, a := range s.Aliases {
		rows = append(rows, statRow{
			Name: n, Command: a.Command, Source: a.Source,
			UsedCount: a.UsedCount, UpdatedAt: a.UpdatedAt,
			CreatedAt: a.CreatedAt, Enabled: a.Enabled,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UsedCount != rows[j].UsedCount {
			return rows[i].UsedCount > rows[j].UsedCount
		}
		return rows[i].Name < rows[j].Name
	})

	mostUsed := []statRow{}
	for _, r := range rows {
		if r.UsedCount == 0 {
			break // sorted desc, so the first 0 means we're done
		}
		mostUsed = append(mostUsed, r)
		if len(mostUsed) >= top {
			break
		}
	}
	neverUsed := []statRow{}
	for _, r := range rows {
		if r.UsedCount == 0 && r.Enabled {
			neverUsed = append(neverUsed, r)
		}
	}
	sort.Slice(neverUsed, func(i, j int) bool { return neverUsed[i].Name < neverUsed[j].Name })

	user, pack, shell := countSources(s)
	totalUses := 0
	for _, r := range rows {
		totalUses += r.UsedCount
	}

	if wantJSON {
		emitStatsJSON(rows, mostUsed, neverUsed, user, pack, shell, totalUses)
		return
	}

	fmt.Println()
	fmt.Printf("  %s %s\n\n", brcyan("👽"), bold("alien stats"))
	fmt.Printf("  %s %s\n", gray("aliases  :"),
		fmt.Sprintf("%d total · %d user · %d pack · %d shell", len(rows), user, pack, shell))
	fmt.Printf("  %s %s\n\n", gray("uses     :"),
		fmt.Sprintf("%d tracked invocations (via `alien run`)", totalUses))

	if len(mostUsed) > 0 {
		fmt.Println(bold("MOST USED"))
		maxName := 0
		for _, r := range mostUsed {
			if len(r.Name) > maxName {
				maxName = len(r.Name)
			}
		}
		for _, r := range mostUsed {
			fmt.Printf("  %s  %s  %s\n",
				bold(brcyan(padRight(r.Name, maxName))),
				dim(fmt.Sprintf("× %-4d", r.UsedCount)),
				truncate(r.Command, 50))
		}
		fmt.Println()
	}

	if len(neverUsed) > 0 {
		fmt.Printf("%s %s\n", bold("NEVER USED"),
			dim(fmt.Sprintf("(%d, cleanup candidates)", len(neverUsed))))
		const showCap = 12
		for i, r := range neverUsed {
			if i >= showCap {
				fmt.Printf("  %s %s\n", dim("…"), dim(fmt.Sprintf("(%d more)", len(neverUsed)-showCap)))
				break
			}
			fmt.Printf("  %s %s  %s\n", gray("○"), padRight(r.Name, 16), gray(truncate(r.Command, 50)))
		}
		fmt.Println()
	}

	if totalUses == 0 && len(rows) > 0 {
		fmt.Printf("%s no tracked usage yet — invoke aliases via %s to record stats.\n",
			dim("hint:"), cyan("alien run <name>"))
	}
}

func emitStatsJSON(all, mostUsed, neverUsed []statRow, user, pack, shell, totalUses int) {
	type entry struct {
		Name      string    `json:"name"`
		Command   string    `json:"command"`
		Source    string    `json:"source"`
		UsedCount int       `json:"used_count"`
		Enabled   bool      `json:"enabled"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	conv := func(rs []statRow) []entry {
		out := make([]entry, 0, len(rs))
		for _, r := range rs {
			out = append(out, entry{
				Name: r.Name, Command: r.Command, Source: r.Source,
				UsedCount: r.UsedCount, Enabled: r.Enabled, UpdatedAt: r.UpdatedAt,
			})
		}
		return out
	}
	payload := map[string]any{
		"totals": map[string]int{
			"aliases": len(all), "user": user, "pack": pack, "shell": shell,
			"tracked_invocations": totalUses,
		},
		"most_used":  conv(mostUsed),
		"never_used": conv(neverUsed),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println(string(data))
}
