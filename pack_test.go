package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePack(t *testing.T) {
	good := `{
  "ufo": {"name": "demo", "version": "1.0.0"},
  "aliases": {"ll": {"command": "ls -al"}}
}`
	p, err := parsePack([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if p.UFO.Name != "demo" || len(p.Aliases) != 1 {
		t.Errorf("unexpected pack: %+v", p)
	}

	cases := []struct {
		body, wantSubstr string
	}{
		{`{"ufo": {"name": ""}, "aliases": {}}`, "name is required"},
		{`{"ufo": {"name": "demo"}, "aliases": {}}`, "aliases section is empty"},
		{`{"ufo": {"name": "bad name"}, "aliases": {"x": {"command": "y"}}}`, "invalid name"},
		{`{"ufo": {"name": "ok"}, "aliases": {"BAD NAME": {"command": "y"}}}`, "invalid alias name"},
		{`{"ufo": {"name": "ok"}, "aliases": {"x": {"command": ""}}}`, "empty command"},
	}
	for _, c := range cases {
		_, err := parsePack([]byte(c.body))
		if err == nil {
			t.Errorf("expected error for %q", c.body)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSubstr) {
			t.Errorf("error = %q; want it to contain %q", err.Error(), c.wantSubstr)
		}
	}
}

func TestApplyInstallAndUninstall(t *testing.T) {
	s := &Store{
		Aliases: map[string]Alias{
			"existing": {Command: "echo existing", Enabled: true},
		},
		Packs: map[string]InstalledPack{},
	}
	p := &Pack{
		UFO: PackMeta{Name: "demo", Version: "1.0.0"},
		Aliases: map[string]PackEntry{
			"a": {Command: "echo a"},
			"b": {Command: "echo b"},
			"c": {Command: "echo c"},
		},
	}

	decisions := defaultDecisions(p, s)
	// Skip "b", rename "c" to "demo-c" (simulating a conflict the user
	// resolved interactively).
	for i, d := range decisions {
		switch d.OriginalName {
		case "b":
			decisions[i].Skip = true
		case "c":
			decisions[i].TargetName = "demo-c"
		}
	}

	installed, skipped, renamed := applyInstall(s, p, "builtin:demo", "", decisions)
	if installed != 2 || skipped != 1 || renamed != 1 {
		t.Errorf("counts = (%d, %d, %d); want (2, 1, 1)", installed, skipped, renamed)
	}
	if _, ok := s.Aliases["a"]; !ok {
		t.Error("alias 'a' should be installed")
	}
	if _, ok := s.Aliases["b"]; ok {
		t.Error("alias 'b' should be skipped")
	}
	if _, ok := s.Aliases["demo-c"]; !ok {
		t.Error("alias 'demo-c' should be installed (renamed)")
	}
	pack := s.Packs["demo"]
	if len(pack.AliasNames) != 2 {
		t.Errorf("pack records %d names; want 2", len(pack.AliasNames))
	}

	// Detach one entry so uninstall keeps it.
	a := s.Aliases["a"]
	a.Source = "" // user has taken over
	s.Aliases["a"] = a

	removed, kept := applyUninstall(s, "demo")
	if removed != 1 || kept != 1 {
		t.Errorf("uninstall counts = (%d, %d); want (1, 1)", removed, kept)
	}
	if _, ok := s.Aliases["existing"]; !ok {
		t.Error("user-owned alias should not be touched by uninstall")
	}
	if _, ok := s.Aliases["a"]; !ok {
		t.Error("detached alias should be kept on uninstall")
	}
	if _, ok := s.Packs["demo"]; ok {
		t.Error("pack record should be removed")
	}
}

const fixturePack = `{
  "ufo": {"name": "demo", "version": "1.0.0"},
  "aliases": {"ll": {"command": "ls -al"}}
}`

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestResolvePackChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fixturePack)
	}))
	defer srv.Close()
	goodSum := sha256Hex([]byte(fixturePack))

	// Unpinned download still works and reports the digest.
	p, _, digest, err := resolvePack(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.UFO.Name != "demo" || digest != goodSum {
		t.Errorf("pack %q digest %q; want demo / %q", p.UFO.Name, digest, goodSum)
	}

	// Correct pin passes; "sha256:" prefix and case are tolerated.
	if _, _, _, err := resolvePack(srv.URL, goodSum); err != nil {
		t.Errorf("good pin rejected: %v", err)
	}
	if _, _, _, err := resolvePack(srv.URL, "SHA256:"+strings.ToUpper(goodSum)); err != nil {
		t.Errorf("prefixed/uppercase pin rejected: %v", err)
	}

	// Wrong pin fails closed.
	bad := strings.Repeat("deadbeef", 8)
	if _, _, _, err := resolvePack(srv.URL, bad); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("bad pin: err = %v; want checksum mismatch", err)
	}

	// Pinning applies to local files too.
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.ufo.json")
	if err := os.WriteFile(path, []byte(fixturePack), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := resolvePack(path, goodSum); err != nil {
		t.Errorf("good pin on file rejected: %v", err)
	}
	if _, _, _, err := resolvePack(path, bad); err == nil {
		t.Error("bad pin on file accepted")
	}
}

func TestFetchURLSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 6 MiB of filler — past the 5 MiB cap.
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for i := 0; i < 6; i++ {
			w.Write(chunk)
		}
	}))
	defer srv.Close()
	data, err := fetchURL(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 5<<20 {
		t.Errorf("fetchURL returned %d bytes; cap is %d", len(data), 5<<20)
	}
}

func TestCLIUfoInstallPinned(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "demo.ufo.json")
	if err := os.WriteFile(path, []byte(fixturePack), 0o644); err != nil {
		t.Fatal(err)
	}
	goodSum := sha256Hex([]byte(fixturePack))

	r := runAlien(t, home, "", "ufo", "install", path, "-y", "--sha256", strings.Repeat("deadbeef", 8))
	if r.code != 1 || !strings.Contains(r.stderr, "checksum mismatch") {
		t.Errorf("bad pin install: exit %d, stderr %q", r.code, r.stderr)
	}
	if len(listJSON(t, home)) != 0 {
		t.Error("failed install left aliases behind")
	}

	mustRun(t, home, "", "ufo", "install", path, "-y", "--sha256", goodSum)
	if e := findEntry(t, listJSON(t, home), "ll"); e.Source != "pack:demo" {
		t.Errorf("installed alias source = %q; want pack:demo", e.Source)
	}
	// The recorded digest lands in aliases.json for later audit.
	raw := readStoreFile(t, home)
	if !strings.Contains(raw, goodSum) {
		t.Errorf("installed pack digest not recorded:\n%s", raw)
	}
}

func TestSuggestNormalization(t *testing.T) {
	// Verify the surface of normalizeCommand that suggest depends on:
	// strict but whitespace-tolerant matching.
	cases := []struct {
		a, b string
		want bool
	}{
		{"git status", "git status", true},
		{"git status", "git  status", true},
		{"git status", "  git   status  ", true},
		{"git status", "git status -sb", false},
		{"git status -sb", "git -sb status", false}, // order matters
	}
	for _, c := range cases {
		eq := normalizeCommand(c.a) == normalizeCommand(c.b)
		if eq != c.want {
			t.Errorf("normalize(%q) == normalize(%q) = %v; want %v", c.a, c.b, eq, c.want)
		}
	}
}
