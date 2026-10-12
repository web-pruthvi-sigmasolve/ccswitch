// Package config holds ccswitch's own settings (account labels, backup
// retention, display preferences) and well-known paths inside its config dir.
package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultBackupRetention is the number of backup sets kept by default.
const DefaultBackupRetention = 20

// Config is the persisted user configuration (config.json in Dir).
type Config struct {
	// Labels maps an account UUID to a user-chosen name.
	Labels map[string]string `json:"labels"`
	// BackupRetention is how many backup sets to keep.
	BackupRetention int `json:"backupRetention"`
	// ASCII forces ASCII-only glyphs in the TUI.
	ASCII bool `json:"ascii"`
	// UpdateCheck is "auto" (the default, also when empty) to allow the daily
	// version check, or "off" to disable it.
	UpdateCheck string `json:"updateCheck"`
}

// Values of Config.UpdateCheck.
const (
	// UpdateCheckAuto allows the once-a-day version check.
	UpdateCheckAuto = "auto"
	// UpdateCheckOff disables the version check.
	UpdateCheckOff = "off"
)

const configFileName = "config.json"

// Dir returns the ccswitch config directory for the given OS, environment and
// home directory.
func Dir(goos string, getenv func(string) string, home string) string {
	switch goos {
	case "windows":
		if v := getenv("APPDATA"); v != "" {
			return filepath.Join(v, "ccswitch")
		}
		return filepath.Join(home, "AppData", "Roaming", "ccswitch")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "ccswitch")
	default:
		if v := getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(v, "ccswitch")
		}
		return filepath.Join(home, ".config", "ccswitch")
	}
}

// DefaultDir returns Dir for the running OS and real environment.
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return Dir(runtime.GOOS, os.Getenv, home)
}

// BackupDir returns the folder that holds backup sets.
func BackupDir(dir string) string { return filepath.Join(dir, "backups") }

// LockPath returns the lock file path used to prevent concurrent runs.
func LockPath(dir string) string { return filepath.Join(dir, "ccswitch.lock") }

func defaults() *Config {
	return &Config{Labels: map[string]string{}, BackupRetention: DefaultBackupRetention}
}

// Load reads dir/config.json. A missing file yields the defaults.
func Load(dir string) (*Config, error) {
	c := defaults()
	data, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", configFileName, err)
	}
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	return c, nil
}

// Save writes dir/config.json atomically (temp file + rename) with mode 0600.
func (c *Config) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, configFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp config: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, configFileName)); err != nil {
		cleanup()
		return fmt.Errorf("replacing config: %w", err)
	}
	return nil
}

// SetLabel stores a label for an account UUID; an empty label removes it.
func (c *Config) SetLabel(uuid, label string) {
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	label = strings.TrimSpace(label)
	if label == "" {
		delete(c.Labels, uuid)
		return
	}
	c.Labels[uuid] = label
}

// MigrateLegacyLabels reads the old bash prototype's accounts.conf ("key=label"
// lines, key being an account UUID or "account/org"). A missing or unreadable
// file yields an empty map. The first line for a key wins, as in the script.
func MigrateLegacyLabels(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			continue
		}
		if _, dup := out[k]; !dup {
			out[k] = v
		}
	}
	return out
}
