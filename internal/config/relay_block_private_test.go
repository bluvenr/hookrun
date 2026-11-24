package config

import (
	"os"
	"path/filepath"
	"testing"
)

// D-08: relay_block_private tightens the relay SSRF policy from "block
// link-local/metadata only" to "also block loopback, RFC1918 and ULA".
// Unset must mean false so local relay clusters keep working by default.

func TestRelayBlockPrivate_DefaultFalse(t *testing.T) {
	s := ServerConfig{}
	if s.IsRelayBlockPrivate() {
		t.Error("unset relay_block_private must default to false")
	}
	cfg := Defaults()
	if cfg.Server.IsRelayBlockPrivate() {
		t.Error("Defaults() must leave relay_block_private false")
	}
}

func TestRelayBlockPrivate_ExplicitValues(t *testing.T) {
	yes, no := true, false
	s := ServerConfig{RelayBlockPrivate: &yes}
	if !s.IsRelayBlockPrivate() {
		t.Error("explicit true must be honored")
	}
	s = ServerConfig{RelayBlockPrivate: &no}
	if s.IsRelayBlockPrivate() {
		t.Error("explicit false must be honored")
	}
}

func TestLoadGlobalConfig_RelayBlockPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: 9000
  relay_registry_token: secret
  relay_block_private: true
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(path)
	if err != nil {
		t.Fatalf("valid config should load: %v", err)
	}
	if !cfg.Server.IsRelayBlockPrivate() {
		t.Error("relay_block_private: true must survive YAML load")
	}
}
