package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/agent"
)

func TestConfigPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")
	data := []byte("id: yaml-agent\nhostname: yaml-host\nserver: yaml:9000\ntoken: 01234567890123456789012345678901\nretry: true\nretry_delay: 10s\nsources:\n  - type: file\n    path: app.log\n    app: yaml-app\n    category: yaml\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var got agent.Config
	cmd := newCommand(func(_ context.Context, cfg agent.Config) error {
		got = cfg
		return nil
	})
	cmd.SetArgs([]string{
		"--config", path,
		"--id", "flag-agent",
		"--server", "flag:9000",
		"--retry=false",
		"--retry-delay", "3s",
		"--journal", "billing.service,billing,service,false",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.ID != "flag-agent" || got.Server != "flag:9000" || got.Retry || got.RetryDelay != 3*time.Second {
		t.Fatalf("flags did not override YAML: %+v", got)
	}
	if got.Hostname != "yaml-host" {
		t.Fatalf("hostname from YAML was not preserved: %+v", got)
	}
	if len(got.Sources) != 1 || got.Sources[0].Type != "journald" || got.Sources[0].Unit != "billing.service" {
		t.Fatalf("source flags did not replace YAML sources: %+v", got.Sources)
	}
}
