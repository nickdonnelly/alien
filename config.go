package main

// User preferences live in $ALIEN_HOME/config.toml (e.g. ~/.config/alien).
// These are behaviour/UI settings that aren't aliases — currently just the
// picker's Tab-insert mode. Like usage.json, config.toml stays OUT of the
// synced store (the sync .gitignore is deny-by-default), so preferences are
// per-machine.
//
// The file is created automatically on first run with every option documented
// by a comment, so it doubles as the reference for what can be tuned. alien
// owns the format: it rewrites the whole file (header + per-option comments +
// current values) on every `config set`, so hand-written comments and unknown
// keys are not preserved. A missing or unreadable file is non-fatal — we fall
// back to the built-in defaults rather than wedge commands that read it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func configPath() string { return filepath.Join(dataDir(), "config.toml") }

// configKeys lists the settable keys (also the TOML key names) in display
// order, each with its built-in default and a help comment written into the
// file. help may span multiple lines.
var configKeys = []struct {
	key, def, help string
}{
	{
		key: "tab-insert",
		def: "name",
		help: "What the picker's Tab key inserts for the selected alias:\n" +
			"  \"name\"    the alias name (e.g. `ll`)\n" +
			"  \"command\" the expanded command (e.g. `ls -al`)\n" +
			"The ALIEN_TAB_INSERT env var overrides this for a single shell.",
	},
}

type Config struct {
	values map[string]string
}

func newConfig() *Config { return &Config{values: map[string]string{}} }

func isConfigKey(key string) bool {
	for _, k := range configKeys {
		if k.key == key {
			return true
		}
	}
	return false
}

func configDefault(key string) string {
	for _, k := range configKeys {
		if k.key == key {
			return k.def
		}
	}
	return ""
}

// canonConfigValue validates and canonicalizes a value for a key.
func canonConfigValue(key, val string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(val))
	switch key {
	case "tab-insert":
		switch v {
		case "name":
			return "name", nil
		case "command", "cmd", "expanded":
			return "command", nil
		}
		return "", fmt.Errorf("invalid value %q for tab-insert (want: name | command)", val)
	}
	return "", fmt.Errorf("unknown config key %q", key)
}

func loadConfig() *Config {
	c := newConfig()
	data, err := os.ReadFile(configPath())
	if err != nil || len(data) == 0 {
		return c
	}
	c.values = parseTOML(data)
	return c
}

func (c *Config) save() error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(c.render()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath())
}

// render produces the full TOML document: a header, then each known option
// preceded by its help comment and written at its current effective value.
func (c *Config) render() string {
	var b strings.Builder
	b.WriteString("# alien configuration\n")
	b.WriteString("# Auto-generated with defaults — edit values below or run\n")
	b.WriteString("# `alien config set <key> <value>`. Comments and unknown keys are\n")
	b.WriteString("# not preserved when alien rewrites this file.\n")
	for _, k := range configKeys {
		b.WriteString("\n")
		for _, line := range strings.Split(k.help, "\n") {
			b.WriteString("# " + line + "\n")
		}
		fmt.Fprintf(&b, "%s = %q\n", k.key, resolveConfig(c, k.key))
	}
	return b.String()
}

// resolveConfig returns the effective value for a key: the stored value, or the
// built-in default when unset.
func resolveConfig(c *Config, key string) string {
	if v, ok := c.values[key]; ok && v != "" {
		return v
	}
	return configDefault(key)
}

// ensureConfig writes config.toml with documented defaults if it doesn't exist
// yet. Best-effort: errors are ignored so it can never break a command.
func ensureConfig() {
	if _, err := os.Stat(configPath()); err == nil {
		return
	}
	_ = newConfig().save()
}

// ---------- minimal flat TOML ----------
//
// The config is a flat table of string-valued keys, so a tiny parser is enough
// and avoids a dependency. It handles `key = "value"` (basic strings with the
// common escapes) and bare values, skips blank lines, `#` comments, and any
// `[table]` headers it doesn't understand.

func parseTOML(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
			continue
		}
		eq := strings.IndexByte(t, '=')
		if eq < 0 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(t[:eq]), `"'`)
		if key == "" {
			continue
		}
		out[key] = parseTOMLValue(t[eq+1:])
	}
	return out
}

func parseTOMLValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 1 && raw[0] == '"' {
		var b strings.Builder
		esc := false
		for i := 1; i < len(raw); i++ {
			ch := raw[i]
			if esc {
				switch ch {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(ch) // covers \" and \\
				}
				esc = false
				continue
			}
			switch ch {
			case '\\':
				esc = true
			case '"':
				return b.String()
			default:
				b.WriteByte(ch)
			}
		}
		return b.String()
	}
	// Bare value: drop any inline comment.
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// ---------- alien config ----------

func cmdConfig(args []string) {
	if len(args) == 0 || args[0] == "list" {
		printConfig()
		return
	}
	switch args[0] {
	case "get":
		if len(args) < 2 {
			errorf("usage: alien config get <key>")
			os.Exit(1)
		}
		key := args[1]
		if !isConfigKey(key) {
			errorf("unknown config key %q", key)
			os.Exit(1)
		}
		// Raw stdout so the shell hook can capture exactly the value.
		fmt.Println(resolveConfig(loadConfig(), key))

	case "set":
		if len(args) < 3 {
			errorf("usage: alien config set <key> <value>")
			os.Exit(1)
		}
		key, val := args[1], args[2]
		canon, err := canonConfigValue(key, val)
		if err != nil {
			errorf("%v", err)
			os.Exit(1)
		}
		c := loadConfig()
		c.values[key] = canon
		if err := c.save(); err != nil {
			errorf("save config: %v", err)
			os.Exit(1)
		}
		successf("set %s = %s", bold(key), canon)

	case "unset":
		if len(args) < 2 {
			errorf("usage: alien config unset <key>")
			os.Exit(1)
		}
		key := args[1]
		if !isConfigKey(key) {
			errorf("unknown config key %q", key)
			os.Exit(1)
		}
		c := loadConfig()
		delete(c.values, key)
		if err := c.save(); err != nil {
			errorf("save config: %v", err)
			os.Exit(1)
		}
		successf("unset %s (now %s)", bold(key), configDefault(key))

	default:
		errorf("unknown subcommand: alien config %s (try get|set|unset|list)", args[0])
		os.Exit(1)
	}
}

func printConfig() {
	c := loadConfig()
	fmt.Println()
	fmt.Printf("  %s %s  %s\n\n", brcyan("👽"), bold("alien config"), gray(configPath()))
	for _, k := range configKeys {
		val := resolveConfig(c, k.key)
		origin := dim("(default)")
		if val != k.def {
			origin = dim("(modified)")
		}
		fmt.Printf("  %s  %s  %s\n",
			bold(brcyan(padRight(k.key, 12))), padRight(val, 10), origin)
		for _, line := range strings.Split(k.help, "\n") {
			fmt.Printf("  %s\n", gray("  "+line))
		}
		fmt.Println()
	}
	fmt.Printf("  %s %s\n\n", dim("change with"), cyan("alien config set <key> <value>"))
}
