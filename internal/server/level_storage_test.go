package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
)

// storeRecord runs one record end to end through the connection handler and
// returns what landed on disk.
func storeRecord(t *testing.T, record protocol.Record) protocol.StoredRecord {
	t.Helper()
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
	if err := encoder.Encode(protocol.Hello{
		Version: protocol.Version, AgentID: "prod-1", Hostname: "api-01",
		Token: token, Apps: []string{"billing"},
	}); err != nil {
		t.Fatal(err)
	}
	var ack protocol.Ack
	if err := protocol.Decode(scanner, &ack); err != nil || !ack.OK {
		t.Fatalf("hello ack: %+v, %v", ack, err)
	}
	if err := encoder.Encode(record); err != nil {
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
	return stored
}

// TestAStatedLevelReachesStorage closes the loop the other half of the way: the
// field is only useful if it survives the hop through the server.
func TestAStatedLevelReachesStorage(t *testing.T) {
	stored := storeRecord(t, protocol.Record{
		Source: "journald", App: "billing", Category: "daemon",
		Line: "connection refused", Level: "3",
	})
	if stored.Level != "3" {
		t.Errorf("the stored level is %q, want %q; the field is being dropped on ingest",
			stored.Level, "3")
	}
}

// TestAnUnstatedLevelLeavesNoFieldOnDisk is the other half: no level in, no
// level key out, so the stored bytes are what they would have been before.
func TestAnUnstatedLevelLeavesNoFieldOnDisk(t *testing.T) {
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
	stored := protocol.StoredRecord{
		ReceivedAt: time.Now().UTC(), AgentID: "prod-1", Hostname: "api-01",
		Source: "journald", App: "billing", Category: "daemon",
		Line: "connection refused",
	}
	if err := run.append(stored); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*", "billing", "prod-1.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("stored file: %v, %v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if jsonHasLevel(data) {
		t.Errorf("a record with no level wrote one: %s", data)
	}
}

func jsonHasLevel(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, ok := probe["level"]
	return ok
}
