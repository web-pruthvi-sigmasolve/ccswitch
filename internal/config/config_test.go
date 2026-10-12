package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

func TestDir(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name string
		goos string
		env  map[string]string
		home string
		want string
	}{
		{"linux default", "linux", nil, "/home/u", filepath.Join("/home/u", ".config", "ccswitch")},
		{"linux xdg", "linux", map[string]string{"XDG_CONFIG_HOME": "/x"}, "/home/u", filepath.Join("/x", "ccswitch")},
		{"windows appdata", "windows", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}, `C:\Users\u`, filepath.Join(`C:\Users\u\AppData\Roaming`, "ccswitch")},
		{"windows fallback", "windows", nil, "/h", filepath.Join("/h", "AppData", "Roaming", "ccswitch")},
		{"darwin", "darwin", nil, "/Users/u", filepath.Join("/Users/u", "Library", "Application Support", "ccswitch")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Dir(tc.goos, env(tc.env), tc.home); got != tc.want {
				t.Errorf("Dir = %q, want %q", got, tc.want)
			}
		})
	}
	if BackupDir("/c") != filepath.Join("/c", "backups") || LockPath("/c") != filepath.Join("/c", "ccswitch.lock") {
		t.Error("BackupDir/LockPath wrong")
	}
	if DefaultDir() == "" {
		t.Error("DefaultDir empty")
	}
}

func TestLoadDefaultsAndRoundtrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "ccswitch")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.BackupRetention != 20 || c.Labels == nil || c.ASCII {
		t.Fatalf("defaults = %+v", c)
	}
	c.SetLabel("u1", "  work ")
	c.SetLabel("u2", "home")
	c.SetLabel("u2", "")
	c.ASCII = true
	c.BackupRetention = 5
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	testutil.WantPerm(t, filepath.Join(dir, "config.json"), 0o600)
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels["u1"] != "work" || len(got.Labels) != 1 || !got.ASCII || got.BackupRetention != 5 {
		t.Errorf("roundtrip = %+v", got)
	}
}

func TestLoadPartialAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"ascii":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || c.BackupRetention != 20 || !c.ASCII || c.Labels == nil {
		t.Fatalf("partial = %+v, %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("corrupt config should error")
	}
}

func TestMigrateLegacyLabels(t *testing.T) {
	p := filepath.Join(t.TempDir(), "accounts.conf")
	content := "# comment\r\nuuid-1=work\r\n\nuuid-2=Me = Home\nuuid-1=dup\nbadline\nempty=\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := MigrateLegacyLabels(p)
	if len(got) != 2 || got["uuid-1"] != "work" || got["uuid-2"] != "Me = Home" {
		t.Errorf("labels = %v", got)
	}
	if m := MigrateLegacyLabels(filepath.Join(t.TempDir(), "nope")); m == nil || len(m) != 0 {
		t.Errorf("missing file = %v", m)
	}
}

func TestUpdateCheckRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil || c.UpdateCheck != "" {
		t.Fatalf("default = %+v, %v", c, err)
	}
	c.UpdateCheck = UpdateCheckOff
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || got.UpdateCheck != UpdateCheckOff {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}
