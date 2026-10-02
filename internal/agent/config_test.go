package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	t.Run("defaults and environment token", func(t *testing.T) {
		t.Setenv("LOGMON_AGENT_TOKEN", strings.Repeat("a", 32))
		cfg, err := LoadConfig("")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server != "127.0.0.1:9000" || !cfg.Retry || cfg.RetryDelay != 2*time.Second || cfg.Token != strings.Repeat("a", 32) {
			t.Fatalf("unexpected defaults: %+v", cfg)
		}
	})

	t.Run("strict YAML and relative path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agent.yaml")
		data := []byte("id: prod-1\nserver: logs:9000\ntoken: 01234567890123456789012345678901\nretry: false\nretry_delay: 3s\nsources:\n  - type: file\n    path: app.log\n    app: billing\n    category: access\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Sources[0].Path != filepath.Join(dir, "app.log") || cfg.Retry || cfg.RetryDelay != 3*time.Second {
			t.Fatalf("unexpected config: %+v", cfg)
		}

		if err := os.WriteFile(path, append(data, []byte("unknown: true\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("expected unknown YAML field error")
		}
	})
}

func TestParseSource(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		spec      string
		wantField string
		want      string
		wantErr   bool
	}{
		{"file", "file", `"/var/log/a,b.log",app,access,true`, "path", "/var/log/a,b.log", false},
		{"journal", "journald", "billing.service,app,service,false", "unit", "billing.service", false},
		{"docker", "docker", "billing-api,app,container,false", "container", "billing-api", false},
		{"few fields", "file", "app.log,app,false", "", "", true},
		{"bad boolean", "file", "app.log,app,access,sometimes", "", "", true},
		{"unknown type", "syslog", "socket,app,system,false", "", "", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := ParseSource(test.kind, test.spec)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseSource() error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil {
				return
			}
			got := map[string]string{"path": source.Path, "unit": source.Unit, "container": source.Container}[test.wantField]
			if got != test.want || source.App != "app" {
				t.Fatalf("unexpected source: %+v", source)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	valid := func() Config {
		return Config{
			ID:         "prod-1",
			Hostname:   "api-01",
			Server:     "logs:9000",
			Token:      strings.Repeat("a", 32),
			RetryDelay: time.Second,
			Sources:    []Source{{Type: "file", Path: "app.log", App: "app", Category: "access"}},
		}
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"invalid id", func(cfg *Config) { cfg.ID = "../prod" }, true},
		{"missing hostname", func(cfg *Config) { cfg.Hostname = "" }, true},
		{"oversized hostname", func(cfg *Config) { cfg.Hostname = strings.Repeat("a", 256) }, true},
		{"max length hostname", func(cfg *Config) { cfg.Hostname = strings.Repeat("a", 255) }, false},
		{"missing server", func(cfg *Config) { cfg.Server = "" }, true},
		{"short token", func(cfg *Config) { cfg.Token = "short" }, true},
		{"invalid retry delay", func(cfg *Config) { cfg.RetryDelay = 0 }, true},
		{"no sources", func(cfg *Config) { cfg.Sources = nil }, true},
		{"invalid app", func(cfg *Config) { cfg.Sources[0].App = "bad/app" }, true},
		{"missing category", func(cfg *Config) { cfg.Sources[0].Category = "" }, true},
		{"missing file", func(cfg *Config) { cfg.Sources[0].Path = "" }, true},
		{"missing journal unit", func(cfg *Config) { cfg.Sources[0] = Source{Type: "journald", App: "app", Category: "service"} }, true},
		{"missing container", func(cfg *Config) { cfg.Sources[0] = Source{Type: "docker", App: "app", Category: "container"} }, true},
		{"unknown source", func(cfg *Config) { cfg.Sources[0].Type = "syslog" }, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid()
			test.mutate(&cfg)
			err := cfg.Validate()
			if test.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !test.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}
