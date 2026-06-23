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
	LastUsed  time.Time
	Last7     int
	Prev7     int
	CreatedAt time.Time
	Enabled   bool
}

// trend classifies recent momentum from the daily buckets: more hits in the
// last 7 days than the 7 before → rising; the inverse (with some prior use)
// → fading. Empty string when there's no signal either way.
func (r statRow) trend() string {
	switch {
	case r.Last7 > r.Prev7 && r.Last7 > 0:
		return "rising"
	case r.Last7 < r.Prev7:
		return "fading"
	default:
		return ""
	}
}

// windowHits sums daily buckets with ages in [fromDaysAgo, toDaysAgo).
func windowHits(daily map[string]int, now time.Time, fromDaysAgo, toDaysAgo int) int {
	total := 0
	for i := fromDaysAgo; i < toDaysAgo; i++ {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		total += daily[day]
	}
	return total
}

// cmdStats summarises usage from the machine-local usage.json. Counts come
// from the shell tracking hook and `alien run`; pending hits are flushed
// first so the numbers are current. Counts are per-machine — sync does not
// carry them.
func cmdStats(args []string) {
	args, wantJSON := extractBoolFlag(args, "--json")
	_, topStr := extractFlag(args, "--top")
	top := 10
	if topStr != "" {
		if n, err := strconv.Atoi(topStr); err == nil && n > 0 {
			top = n
		}
	}

	_ = trackFlush() // best-effort: stats should reflect hits still in the log

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	usage := loadUsage()
	now := time.Now()

	rows := make([]statRow, 0, len(s.Aliases))
	for n, a := range s.Aliases {
		e := usage.Aliases[n]
		rows = append(rows, statRow{
			Name: n, Command: a.Command, Source: a.Source,
			UsedCount: e.Count, LastUsed: e.LastUsed,
			Last7:     windowHits(e.Daily, now, 0, 7),
			Prev7:     windowHits(e.Daily, now, 7, 14),
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
		fmt.Sprintf("%d tracked invocations (shell + alien run, this machine)", totalUses))

	if len(mostUsed) > 0 {
		fmt.Println(bold("MOST USED"))
		maxName := 0
		for _, r := range mostUsed {
			if len(r.Name) > maxName {
				maxName = len(r.Name)
			}
		}
		for _, r := range mostUsed {
			extra := relativeAge(r.LastUsed, now)
			if tr := r.trend(); tr != "" {
				extra += " · " + tr
			}
			fmt.Printf("  %s  %s  %s  %s\n",
				bold(brcyan(padRight(r.Name, maxName))),
				dim(fmt.Sprintf("× %-4d", r.UsedCount)),
				truncate(r.Command, 44),
				gray(extra))
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
		fmt.Printf("%s no tracked usage yet — use your aliases in a hooked shell, or via %s.\n",
			dim("hint:"), cyan("alien run <name>"))
	}
}

// relativeAge renders a compact "3d ago"-style label; empty for zero times.
func relativeAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func emitStatsJSON(all, mostUsed, neverUsed []statRow, user, pack, shell, totalUses int) {
	type entry struct {
		Name      string    `json:"name"`
		Command   string    `json:"command"`
		Source    string    `json:"source"`
		UsedCount int       `json:"used_count"`
		Enabled   bool      `json:"enabled"`
		LastUsed  time.Time `json:"last_used"`
		Last7Days int       `json:"last_7_days"`
		Trend     string    `json:"trend"`
	}
	conv := func(rs []statRow) []entry {
		out := make([]entry, 0, len(rs))
		for _, r := range rs {
			out = append(out, entry{
				Name: r.Name, Command: r.Command, Source: r.Source,
				UsedCount: r.UsedCount, Enabled: r.Enabled,
				LastUsed: r.LastUsed, Last7Days: r.Last7, Trend: r.trend(),
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
