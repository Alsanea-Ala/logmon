package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Alsanea-Ala/logmon/internal/server"
)

func TestConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	data := []byte("listen: yaml:9000\ndata_dir: yaml-data\nmax_connections: 10\nagents:\n  prod-1:\n    token_sha256: " + server.TokenHash("01234567890123456789012345678901") + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var got server.Config
	cmd := newCommand(func(_ context.Context, cfg server.Config, _ bool) error {
		got = cfg
		return nil
	})
	cmd.SetArgs([]string{"--config", path, "--listen", "flag:9000", "--data-dir", "flag-data", "--max-connections", "20"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.Listen != "flag:9000" || got.DataDir != "flag-data" || got.MaxConnections != 20 || len(got.Agents) != 1 {
		t.Fatalf("flags did not override YAML: %+v", got)
	}
}
