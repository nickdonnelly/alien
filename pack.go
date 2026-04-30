package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed packs/*.ufo.json
var builtinPacksFS embed.FS

// Pack is the on-disk pack format. The wire schema mirrors what we ship in
// `packs/*.ufo.json` and what `alien ufo export` produces.
type Pack struct {
	UFO     PackMeta             `json:"ufo"`
	Aliases map[string]PackEntry `json:"aliases"`
}

type PackMeta struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
}

type PackEntry struct {
	Command string   `json:"command"`
	Comment string   `json:"comment,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

func parsePack(data []byte) (*Pack, error) {
	var p Pack
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse pack: %w", err)
	}
	if p.UFO.Name == "" {
		return nil, errors.New("pack: ufo.name is required")
	}
	if !validAliasName(p.UFO.Name) {
		return nil, fmt.Errorf("pack: invalid name %q", p.UFO.Name)
	}
	if len(p.Aliases) == 0 {
		return nil, errors.New("pack: aliases section is empty")
	}
	for name, entry := range p.Aliases {
		if !validAliasName(name) {
			return nil, fmt.Errorf("pack: invalid alias name %q", name)
		}
		if strings.TrimSpace(entry.Command) == "" {
			return nil, fmt.Errorf("pack: alias %q has empty command", name)
		}
	}
	return &p, nil
}

// builtinPackNames returns the sorted list of bundled pack names.
func builtinPackNames() []string {
	entries, err := builtinPacksFS.ReadDir("packs")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := strings.TrimSuffix(e.Name(), ".ufo.json")
		if n == e.Name() {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func loadBuiltinPack(name string) ([]byte, bool) {
	data, err := builtinPacksFS.ReadFile("packs/" + name + ".ufo.json")
	if err != nil {
		return nil, false
	}
	return data, true
}

// resolvePack loads a pack from a built-in name, local path, or http(s) URL.
// Returns the parsed pack along with a human-readable origin descriptor that
// will be stored in InstalledPack.Source.
func resolvePack(ref string) (*Pack, string, error) {
	switch {
	case strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://"):
		data, err := fetchURL(ref)
		if err != nil {
			return nil, "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(os.Stderr, "%s downloaded %s (%d bytes, sha256 %s)\n",
			brcyan("👽"), ref, len(data), hex.EncodeToString(sum[:8]))
		p, err := parsePack(data)
		return p, ref, err

	case strings.ContainsAny(ref, "/.") || filepath.IsAbs(ref):
		data, err := os.ReadFile(ref)
		if err != nil {
			return nil, "", err
		}
		p, err := parsePack(data)
		abs, _ := filepath.Abs(ref)
		return p, abs, err

	default:
		// Built-in.
		if data, ok := loadBuiltinPack(ref); ok {
			p, err := parsePack(data)
			return p, "builtin:" + ref, err
		}
		return nil, "", fmt.Errorf("no built-in pack named %q (try `alien ufo list`)", ref)
	}
}

func fetchURL(rawurl string) ([]byte, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http/https URLs are supported, got %q", u.Scheme)
	}
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(rawurl)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %s", rawurl, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MiB cap
}

// ConflictKind classifies why a pack alias collides with an existing entry.
type ConflictKind int

const (
	NoConflict ConflictKind = iota
	ConflictUser
	ConflictPack
	ConflictShell
)

func (c ConflictKind) String() string {
	switch c {
	case ConflictUser:
		return "user"
	case ConflictPack:
		return "pack"
	case ConflictShell:
		return "shell"
	default:
		return "none"
	}
}

// InstallDecision is the per-entry outcome of the install browser. The TUI
// fills these in based on user toggles/renames; the non-interactive path
// builds them with sensible defaults.
type InstallDecision struct {
	OriginalName string
	TargetName   string
	Skip         bool
	Conflict     ConflictKind
	Existing     *Alias
	Entry        PackEntry
}

func defaultDecisions(p *Pack, s *Store) []InstallDecision {
	names := make([]string, 0, len(p.Aliases))
	for n := range p.Aliases {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]InstallDecision, 0, len(names))
	for _, n := range names {
		d := InstallDecision{
			OriginalName: n,
			TargetName:   n,
			Entry:        p.Aliases[n],
		}
		if existing, ok := s.Aliases[n]; ok {
			d.Existing = &existing
			switch {
			case existing.Source == "":
				d.Conflict = ConflictUser
			case strings.HasPrefix(existing.Source, "pack:"):
				d.Conflict = ConflictPack
			case existing.Source == "shell":
				d.Conflict = ConflictShell
			}
		}
		out = append(out, d)
	}
	return out
}

// applyInstall writes the chosen decisions to the store under Source=pack:<name>
// and records an InstalledPack entry for clean uninstall later.
func applyInstall(s *Store, p *Pack, packOrigin string, decisions []InstallDecision) (installed int, skipped int, renamed int) {
	source := "pack:" + p.UFO.Name
	now := time.Now()
	claimed := make([]string, 0, len(decisions))
	for _, d := range decisions {
		if d.Skip {
			skipped++
			continue
		}
		if d.TargetName != d.OriginalName {
			renamed++
		}
		s.Aliases[d.TargetName] = Alias{
			Command:   d.Entry.Command,
			Comment:   d.Entry.Comment,
			Enabled:   true,
			Source:    source,
			Tags:      d.Entry.Tags,
			CreatedAt: now,
			UpdatedAt: now,
		}
		claimed = append(claimed, d.TargetName)
		installed++
	}
	if s.Packs == nil {
		s.Packs = map[string]InstalledPack{}
	}
	sort.Strings(claimed)
	s.Packs[p.UFO.Name] = InstalledPack{
		Name:        p.UFO.Name,
		Version:     p.UFO.Version,
		Description: p.UFO.Description,
		Source:      packOrigin,
		InstalledAt: now,
		AliasNames:  claimed,
	}
	return
}

// applyUninstall removes pack-claimed aliases that still belong to the pack.
// Entries the user promoted away (different Source) are left untouched.
func applyUninstall(s *Store, packName string) (removed int, kept int) {
	pack, ok := s.Packs[packName]
	if !ok {
		return 0, 0
	}
	source := "pack:" + packName
	for _, name := range pack.AliasNames {
		a, ok := s.Aliases[name]
		if !ok {
			continue
		}
		if a.Source != source {
			kept++
			continue
		}
		delete(s.Aliases, name)
		removed++
	}
	delete(s.Packs, packName)
	return
}

// ---------- subcommands ----------

func cmdUfo(args []string) {
	if len(args) == 0 {
		cmdUfoList(nil)
		return
	}
	switch args[0] {
	case "list", "ls":
		cmdUfoList(args[1:])
	case "show", "info":
		cmdUfoShow(args[1:])
	case "install", "add":
		cmdUfoInstall(args[1:])
	case "uninstall", "remove", "rm":
		cmdUfoUninstall(args[1:])
	case "create":
		cmdUfoCreate(args[1:])
	case "export":
		cmdUfoExport(args[1:])
	default:
		errorf("unknown subcommand: alien ufo %s", args[0])
		fmt.Fprintln(os.Stderr, "  available: list, show, install, uninstall, create, export")
		os.Exit(1)
	}
}

func cmdUfoList(_ []string) {
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println(bold("INSTALLED"))
	if len(s.Packs) == 0 {
		fmt.Printf("  %s\n", dim("(none)"))
	} else {
		names := make([]string, 0, len(s.Packs))
		for n := range s.Packs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ip := s.Packs[n]
			ver := ip.Version
			if ver == "" {
				ver = "?"
			}
			fmt.Printf("  %s %s  %s  %s\n",
				green("●"),
				bold(brcyan(padRight(n, 12))),
				dim("v"+ver),
				gray(fmt.Sprintf("%d aliases · %s", len(ip.AliasNames), ip.Source)))
		}
	}

	fmt.Println()
	fmt.Println(bold("BUILT-IN AVAILABLE"))
	for _, n := range builtinPackNames() {
		if _, installed := s.Packs[n]; installed {
			continue
		}
		data, _ := loadBuiltinPack(n)
		p, err := parsePack(data)
		if err != nil {
			continue
		}
		fmt.Printf("  %s %s  %s  %s\n",
			gray("○"),
			bold(brcyan(padRight(n, 12))),
			dim("v"+p.UFO.Version),
			gray(p.UFO.Description))
	}
	fmt.Println()
}

func cmdUfoShow(args []string) {
	if len(args) < 1 {
		errorf("usage: alien ufo show <pack>")
		os.Exit(1)
	}
	p, origin, err := resolvePack(args[0])
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Printf("  %s %s  %s\n", brcyan("👽"), bold(brcyan(p.UFO.Name)), dim("v"+p.UFO.Version))
	if p.UFO.Description != "" {
		fmt.Printf("  %s\n", p.UFO.Description)
	}
	if p.UFO.Author != "" {
		fmt.Printf("  %s %s\n", gray("by"), p.UFO.Author)
	}
	fmt.Printf("  %s %s\n\n", gray("from"), origin)

	maxName := 0
	for n := range p.Aliases {
		if len(n) > maxName {
			maxName = len(n)
		}
	}
	names := make([]string, 0, len(p.Aliases))
	for n := range p.Aliases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := p.Aliases[n]
		comment := ""
		if e.Comment != "" {
			comment = "  " + gray("# "+e.Comment)
		}
		fmt.Printf("  %s  %s%s\n",
			bold(brcyan(padRight(n, maxName))),
			truncate(e.Command, 60),
			comment)
	}
	fmt.Println()
}

func cmdUfoInstall(args []string) {
	var nonInteractive bool
	positional := []string{}
	for _, a := range args {
		switch a {
		case "-y", "--yes", "--non-interactive":
			nonInteractive = true
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 1 {
		errorf("usage: alien ufo install <pack-or-path>")
		os.Exit(1)
	}

	p, origin, err := resolvePack(positional[0])
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	decisions := defaultDecisions(p, s)

	if nonInteractive {
		// Auto-deconflict: keep all selected, rename conflicts to <pack>-<name>.
		for i := range decisions {
			d := &decisions[i]
			if d.Conflict == ConflictUser || d.Conflict == ConflictPack {
				d.TargetName = p.UFO.Name + "-" + d.OriginalName
				if _, clash := s.Aliases[d.TargetName]; clash {
					d.Skip = true
				}
			}
		}
	} else {
		decisions, err = runPackTUI(p, decisions)
		if err != nil {
			errorf("%v", err)
			os.Exit(1)
		}
		if decisions == nil {
			infof("install cancelled")
			return
		}
	}

	var installed, skipped, renamed int
	if err := updateStore(func(s *Store) error {
		installed, skipped, renamed = applyInstall(s, p, origin, decisions)
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("installed pack %s — %d added, %d renamed, %d skipped",
		bold(brcyan(p.UFO.Name)), installed, renamed, skipped)
}

func cmdUfoUninstall(args []string) {
	if len(args) < 1 {
		errorf("usage: alien ufo uninstall <pack>")
		os.Exit(1)
	}
	name := args[0]
	var removed, kept int
	if err := updateStore(func(s *Store) error {
		if _, ok := s.Packs[name]; !ok {
			errorf("pack %s is not installed", bold(name))
			os.Exit(1)
		}
		removed, kept = applyUninstall(s, name)
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("uninstalled %s — %d aliases removed, %d kept (you'd modified them)",
		bold(brcyan(name)), removed, kept)
}

func cmdUfoCreate(args []string) {
	if len(args) < 1 {
		errorf("usage: alien ufo create <name> [--out file]")
		os.Exit(1)
	}
	name := args[0]
	if !validAliasName(name) {
		errorf("invalid pack name %q", name)
		os.Exit(1)
	}
	var out string
	for i := 1; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			out = args[i+1]
			i++
		}
	}

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	p := &Pack{
		UFO: PackMeta{
			Name:        name,
			Version:     "1.0.0",
			Description: "Personal pack — generated by alien ufo create",
		},
		Aliases: map[string]PackEntry{},
	}
	for n, a := range s.Aliases {
		// Only export user-managed entries; shell/pack ones aren't this user's
		// to redistribute.
		if a.Source != "" {
			continue
		}
		p.Aliases[n] = PackEntry{
			Command: a.Command,
			Comment: a.Comment,
			Tags:    a.Tags,
		}
	}
	if len(p.Aliases) == 0 {
		errorf("no user-managed aliases to export")
		os.Exit(1)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	if out == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("wrote pack to %s (%d aliases)", out, len(p.Aliases))
}

func cmdUfoExport(args []string) {
	if len(args) < 1 {
		errorf("usage: alien ufo export <installed-pack> [--out file]")
		os.Exit(1)
	}
	name := args[0]
	var out string
	for i := 1; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			out = args[i+1]
			i++
		}
	}

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	ip, ok := s.Packs[name]
	if !ok {
		errorf("pack %s is not installed", bold(name))
		os.Exit(1)
	}
	source := "pack:" + name
	p := &Pack{
		UFO: PackMeta{
			Name:        ip.Name,
			Version:     ip.Version,
			Description: ip.Description,
		},
		Aliases: map[string]PackEntry{},
	}
	for _, n := range ip.AliasNames {
		a, ok := s.Aliases[n]
		if !ok || a.Source != source {
			continue
		}
		p.Aliases[n] = PackEntry{
			Command: a.Command,
			Comment: a.Comment,
			Tags:    a.Tags,
		}
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	if out == "" {
		fmt.Println(string(data))
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("wrote %s pack to %s", name, out)
}
