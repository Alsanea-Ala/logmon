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

type Record struct {
	Source     string     `json:"source"`
	App        string     `json:"app"`
	Category   string     `json:"category"`
	Line       string     `json:"line"`
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
}

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
