package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
)

func TestAuthenticatedRecordIsStoredBeforeAck(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	token := "01234567890123456789012345678901"
	run := &runtime{
		config: Config{Agents: map[string]AllowedAgent{"prod-1": {TokenSHA256: TokenHash(token)}}},
		root:   root,
		agents: make(map[string]agentStatus),
	}
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		run.handle(serverConn)
		close(done)
	}()
	defer func() {
		clientConn.Close()
		<-done
	}()

	encoder := json.NewEncoder(clientConn)
	scanner := protocol.Scanner(clientConn)
	if err := encoder.Encode(protocol.Hello{Version: protocol.Version, AgentID: "prod-1", Hostname: "api-01", Token: token, Apps: []string{"billing"}}); err != nil {
		t.Fatal(err)
	}
	var ack protocol.Ack
	if err := protocol.Decode(scanner, &ack); err != nil || !ack.OK {
		t.Fatalf("hello ack: %+v, %v", ack, err)
	}
	if err := encoder.Encode(protocol.Record{Source: "file", App: "billing", Category: "error", Line: "payment failed"}); err != nil {
		t.Fatal(err)
	}
	if err := protocol.Decode(scanner, &ack); err != nil || !ack.OK {
		t.Fatalf("record ack: %+v, %v", ack, err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*", "billing", "prod-1.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("stored file: %v, %v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var stored protocol.StoredRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AgentID != "prod-1" || stored.Hostname != "api-01" || stored.Line != "payment failed" || time.Since(stored.ReceivedAt) > time.Minute {
		t.Fatalf("unexpected stored record: %+v", stored)
	}
}

func TestLoadAndValidateConfig(t *testing.T) {
	t.Run("strict YAML and relative data directory", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "server.yaml")
		data := []byte("listen: 127.0.0.1:9000\ndata_dir: stored\nmax_connections: 10\nagents:\n  prod-1:\n    token_sha256: " + TokenHash(strings.Repeat("a", 32)) + "\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DataDir != filepath.Join(dir, "stored") || cfg.MaxConnections != 10 {
			t.Fatalf("unexpected config: %+v", cfg)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, append(data, []byte("unknown: true\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatal("expected unknown YAML field error")
		}
	})

	valid := func() Config {
		return Config{
			Listen:         "127.0.0.1:9000",
			DataDir:        "data",
			MaxConnections: 10,
			Agents:         map[string]AllowedAgent{"prod-1": {TokenSHA256: TokenHash(strings.Repeat("a", 32))}},
		}
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"valid", func(*Config) {}},
		{"missing listen", func(cfg *Config) { cfg.Listen = "" }},
		{"missing data directory", func(cfg *Config) { cfg.DataDir = "" }},
		{"invalid connection limit", func(cfg *Config) { cfg.MaxConnections = 0 }},
		{"no agents", func(cfg *Config) { cfg.Agents = nil }},
		{"invalid agent id", func(cfg *Config) {
			cfg.Agents = map[string]AllowedAgent{"../prod": {TokenSHA256: TokenHash(strings.Repeat("a", 32))}}
		}},
		{"invalid token hash", func(cfg *Config) { cfg.Agents["prod-1"] = AllowedAgent{TokenSHA256: "bad"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid()
			test.mutate(&cfg)
			err := cfg.Validate()
			if test.name == "valid" && err != nil {
				t.Fatal(err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAuthenticate(t *testing.T) {
	token := strings.Repeat("a", 32)
	run := &runtime{config: Config{Agents: map[string]AllowedAgent{"prod-1": {TokenSHA256: TokenHash(token)}}}}
	valid := func() protocol.Hello {
		return protocol.Hello{Version: protocol.Version, AgentID: "prod-1", Hostname: "api-01", Token: token, Apps: []string{"billing"}}
	}
	tests := []struct {
		name   string
		mutate func(*protocol.Hello)
	}{
		{"valid", func(*protocol.Hello) {}},
		{"wrong version", func(hello *protocol.Hello) { hello.Version++ }},
		{"invalid id", func(hello *protocol.Hello) { hello.AgentID = "../prod" }},
		{"unknown id", func(hello *protocol.Hello) { hello.AgentID = "prod-2" }},
		{"missing hostname", func(hello *protocol.Hello) { hello.Hostname = "" }},
		{"long hostname", func(hello *protocol.Hello) { hello.Hostname = strings.Repeat("a", 256) }},
		{"bad app", func(hello *protocol.Hello) { hello.Apps = []string{"bad/app"} }},
		{"wrong token", func(hello *protocol.Hello) { hello.Token = strings.Repeat("b", 32) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hello := valid()
			test.mutate(&hello)
			got := run.authenticate(hello)
			if got != (test.name == "valid") {
				t.Fatalf("authenticate() = %v", got)
			}
		})
	}
}

func TestValidateRecord(t *testing.T) {
	valid := func() protocol.Record {
		return protocol.Record{Source: "file", App: "billing", Category: "error", Line: "failed"}
	}
	tests := []struct {
		name   string
		mutate func(*protocol.Record)
	}{
		{"valid", func(*protocol.Record) {}},
		{"undeclared app", func(record *protocol.Record) { record.App = "auth" }},
		{"invalid app", func(record *protocol.Record) { record.App = "bad/app" }},
		{"missing category", func(record *protocol.Record) { record.Category = "" }},
		{"long category", func(record *protocol.Record) { record.Category = strings.Repeat("a", 129) }},
		{"large line", func(record *protocol.Record) { record.Line = strings.Repeat("a", protocol.MaxLineSize+1) }},
		{"unknown source", func(record *protocol.Record) { record.Source = "syslog" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid()
			test.mutate(&record)
			err := validateRecord(record, map[string]bool{"billing": true})
			if test.name == "valid" && err != nil {
				t.Fatal(err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestReconnectReplacesOldConnection(t *testing.T) {
	run := &runtime{agents: make(map[string]agentStatus)}
	hello := protocol.Hello{AgentID: "prod-1", Hostname: "api-01", Apps: []string{"billing"}}
	oldServer, oldClient := net.Pipe()
	defer oldClient.Close()
	newServer, newClient := net.Pipe()
	defer newServer.Close()
	defer newClient.Close()

	run.register(hello, oldServer)
	run.register(hello, newServer)
	run.disconnect(hello.AgentID, oldServer)
	if status := run.agents[hello.AgentID]; !status.Connected || status.conn != newServer {
		t.Fatalf("stale disconnect replaced current status: %+v", status)
	}
	run.disconnect(hello.AgentID, newServer)
	if run.agents[hello.AgentID].Connected {
		t.Fatal("current disconnect did not mark agent offline")
	}
}
