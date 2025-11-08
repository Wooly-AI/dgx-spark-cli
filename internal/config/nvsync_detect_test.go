package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNVSyncProfileReader(t *testing.T) {
	t.Run("parses profile blocks", func(t *testing.T) {
		dir := t.TempDir()
		keyPath := filepath.Join(dir, "nvsync.key")
		if err := os.WriteFile(keyPath, []byte("test"), 0600); err != nil {
			t.Fatalf("write key: %v", err)
		}

		config := fmt.Sprintf(`
Host 192.168.0.10
    Hostname 192.168.0.10
    User alice
    Port 2222
    IdentityFile "%s"
`, keyPath)

		profiles, err := parseNVSyncProfileReader(strings.NewReader(config))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(profiles) != 1 {
			t.Fatalf("expected 1 profile, got %d", len(profiles))
		}
		profile := profiles[0]
		if profile.Host != "192.168.0.10" {
			t.Fatalf("unexpected host %q", profile.Host)
		}
		if profile.User != "alice" {
			t.Fatalf("unexpected user %q", profile.User)
		}
		if profile.Port != 2222 {
			t.Fatalf("unexpected port %d", profile.Port)
		}
		if profile.IdentityFile != keyPath {
			t.Fatalf("unexpected identity %q", profile.IdentityFile)
		}
	})
}
