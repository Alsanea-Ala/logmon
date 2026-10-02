package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Alsanea-Ala/logmon/internal/protocol"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTUINavigationAndSafeOutput(t *testing.T) {
	now := time.Now().UTC()
	m := newModel(t.TempDir())
	m.files = []logFile{{Date: now.Format(time.DateOnly), App: "billing", Agent: "prod-1", Path: "unused"}}
	m.records = []protocol.StoredRecord{
		{ReceivedAt: now, Category: "access", Line: "ok"},
		{ReceivedAt: now, Category: "error", Line: "bad\x1b[2J"},
	}
	m.categories = recordCategories(m.records)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if m.categories[m.categoryIndex] != "access" || len(m.filtered()) != 1 {
		t.Fatalf("category navigation failed: %+v", m.categories)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if strings.Contains(m.View(), "\x1b") {
		t.Fatal("view contains terminal escape sequence")
	}
}

func TestScanFilesSortsAndFilters(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "2026-09-12", "billing", "prod-2.jsonl"),
		filepath.Join(dir, "2026-09-13", "auth", "prod-1.jsonl"),
		filepath.Join(dir, "2026-09-13", "billing", "prod-1.jsonl"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-13", "billing", "ignore.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	message := scanFiles(dir, 7)().(scanMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	if message.version != 7 || len(message.files) != 3 {
		t.Fatalf("unexpected scan: %+v", message)
	}
	got := make([]string, len(message.files))
	for i, file := range message.files {
		got[i] = file.Date + "/" + file.App + "/" + file.Agent
	}
	if strings.Join(got, ",") != "2026-09-13/auth/prod-1,2026-09-13/billing/prod-1,2026-09-12/billing/prod-2" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestLoadRecordsKeepsLatestAndReportsMalformedData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for i := 0; i < maxVisibleRecords+2; i++ {
		if err := encoder.Encode(protocol.StoredRecord{Line: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	message := loadRecords(path, 4)().(loadMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	if message.version != 4 || len(message.records) != maxVisibleRecords || message.records[0].Line != "2" || message.records[len(message.records)-1].Line != "1001" {
		t.Fatalf("unexpected loaded record window: first=%q last=%q len=%d", message.records[0].Line, message.records[len(message.records)-1].Line, len(message.records))
	}

	malformed := filepath.Join(dir, "malformed.jsonl")
	if err := os.WriteFile(malformed, []byte("{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if message := loadRecords(malformed, 5)().(loadMsg); message.err == nil {
		t.Fatal("expected malformed record error")
	}
}

func TestTUIRejectsStaleResultsAndMergesLiveRecords(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), now.Format(time.DateOnly), "billing", "prod-1.jsonl")
	m := newModel(filepath.Dir(filepath.Dir(filepath.Dir(path))))
	m.files = []logFile{{Date: now.Format(time.DateOnly), App: "billing", Agent: "prod-1", Path: path}}
	m.loadVersion = 2
	m.scanVersion = 2
	m.loadingPath = path
	live := protocol.StoredRecord{ReceivedAt: now, AgentID: "prod-1", App: "billing", Category: "error", Line: "live"}
	history := protocol.StoredRecord{ReceivedAt: now.Add(-time.Second), AgentID: "prod-1", App: "billing", Category: "error", Line: "history"}

	m.Update(scanMsg{version: 1})
	if len(m.files) != 1 {
		t.Fatal("stale scan replaced current files")
	}
	m.Update(recordMsg(live))
	m.Update(loadMsg{version: 1, path: path, records: []protocol.StoredRecord{history}})
	if len(m.records) != 1 || m.records[0].Line != "live" {
		t.Fatalf("stale load replaced live data: %+v", m.records)
	}
	m.Update(loadMsg{version: 2, path: path, records: []protocol.StoredRecord{history, live}})
	if len(m.records) != 2 || m.records[0].Line != "history" || m.records[1].Line != "live" {
		t.Fatalf("live/history merge failed: %+v", m.records)
	}
}
