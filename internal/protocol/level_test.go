package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestLevelIsOmittedWhenUnstated is the compatibility guarantee.
//
// Every record written before this field existed has no level, and the stored
// JSONL has to keep decoding and keep looking the same. A field that serialises
// as "level":"" on every old record would change every line of every existing
// file on the next write and make the diff meaningless.
func TestLevelIsOmittedWhenUnstated(t *testing.T) {
	encoded, err := json.Marshal(Record{
		Source:   "journald",
		App:      "nginx",
		Category: "access",
		Line:     "GET / 200",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "level") {
		t.Errorf("an unstated level was serialised: %s", encoded)
	}

	encoded, err = json.Marshal(StoredRecord{
		ReceivedAt: time.Unix(0, 0).UTC(),
		AgentID:    "prod-1",
		Source:     "file",
		App:        "nginx",
		Category:   "access",
		Line:       "GET / 200",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "level") {
		t.Errorf("an unstated level was serialised: %s", encoded)
	}
}

// TestLevelRoundTrips is the ordinary case.
func TestLevelRoundTrips(t *testing.T) {
	stamp := time.Unix(0, 0).UTC()
	original := Record{
		Source: "journald", App: "nginx", Category: "daemon",
		Line: "connection refused", Level: "3", SourceTime: &stamp,
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"level":"3"`) {
		t.Errorf("the level did not serialise: %s", encoded)
	}

	var decoded Record
	if err := Decode(Scanner(strings.NewReader(string(encoded))), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Level != "3" {
		t.Errorf("the level did not survive the round trip: %q", decoded.Level)
	}
}

// TestAStoredRecordWithoutALevelStillDecodes is the year-of-existing-JSONL case,
// stated directly rather than inferred from the omitempty test.
//
// A file written before this field existed must load, and must load with an
// empty Level so the caller falls back rather than treating the absence as a
// level it cannot read.
func TestAStoredRecordWithoutALevelStillDecodes(t *testing.T) {
	const oldLine = `{"received_at":"2026-10-15T12:00:00Z","agent_id":"prod-1",` +
		`"hostname":"web-01","source":"journald","app":"nginx",` +
		`"category":"daemon","line":"error: disk full"}`

	var stored StoredRecord
	if err := Decode(Scanner(strings.NewReader(oldLine)), &stored); err != nil {
		t.Fatalf("a record written before Level existed no longer decodes: %v", err)
	}
	if stored.Level != "" {
		t.Errorf("an absent level decoded to %q, want empty so the caller falls back", stored.Level)
	}
	if stored.Line != "error: disk full" {
		t.Errorf("the rest of the record did not survive: %+v", stored)
	}
}

// TestVersionStaysAtOne pins the reasoning for not bumping it.
//
// A field that is absent means "not stated", which is exactly what every old
// record already meant, and omitempty keeps the old bytes identical. Nothing
// about the old shape changed, so there is nothing for a peer to negotiate and
// nothing that justifies refusing to talk to an older agent.
func TestVersionStaysAtOne(t *testing.T) {
	if Version != 1 {
		t.Errorf("Version is %d; it should still be 1 because the old shape is unchanged", Version)
	}
	if MaxLevelSize < 16 {
		t.Errorf("MaxLevelSize is %d, too small for the longest syslog name", MaxLevelSize)
	}
}
