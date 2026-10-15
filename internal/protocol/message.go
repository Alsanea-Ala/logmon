package protocol

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

const (
	Version      = 1
	MaxFrameSize = 1 << 20
	MaxLineSize  = MaxFrameSize / 8
)

type Hello struct {
	Version  int      `json:"version"`
	AgentID  string   `json:"agent_id"`
	Hostname string   `json:"hostname"`
	Token    string   `json:"token"`
	Apps     []string `json:"apps"`
}

// Level is the severity the source stated for this line, verbatim, or empty if
// the source did not state one.
//
// Journald emits PRIORITY as its syslog number, 0 emerg through 7 debug, and
// that is what is carried here. File and Docker sources have no severity of
// their own and leave it empty; the server decides what to do about that, and
// guessing here would put an inference on the wire where it looks like a fact.
//
// omitempty, so a record with no stated level is byte-identical to what a
// pre-Level agent sent. Existing stored files need no migration, and Version
// stays 1 because nothing about the old shape changed: an absent field means
// "not stated", which is exactly what every old record already meant.
type Record struct {
	Source     string     `json:"source"`
	App        string     `json:"app"`
	Category   string     `json:"category"`
	Line       string     `json:"line"`
	Level      string     `json:"level,omitempty"`
	SourceTime *time.Time `json:"source_time,omitempty"`
}

type StoredRecord struct {
	ReceivedAt time.Time  `json:"received_at"`
	SourceTime *time.Time `json:"source_time,omitempty"`
	AgentID    string     `json:"agent_id"`
	Hostname   string     `json:"hostname"`
	Source     string     `json:"source"`
	App        string     `json:"app"`
	Category   string     `json:"category"`
	Line       string     `json:"line"`
	Level      string     `json:"level,omitempty"`
}

// MaxLevelSize bounds Level on the wire, for the same reason Category is
// bounded: an agent is a remote party and a field nothing parses to a fixed
// size is a field worth bounding.
const MaxLevelSize = 16

type Ack struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func ValidName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r != '-' && r != '_' && r != '.' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func Scanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), MaxFrameSize)
	return s
}

func Decode(scanner *bufio.Scanner, value any) error {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	return json.Unmarshal(scanner.Bytes(), value)
}
