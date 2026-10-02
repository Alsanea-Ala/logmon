package protocol

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"simple", "prod-1", true},
		{"all allowed punctuation", "api_1.example", true},
		{"maximum length", strings.Repeat("a", 128), true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", 129), false},
		{"slash", "../prod", false},
		{"space", "prod 1", false},
		{"unicode", "prod-\u00e9", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidName(test.value); got != test.want {
				t.Fatalf("ValidName(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var ack Ack
		if err := Decode(Scanner(strings.NewReader("{\"ok\":true}\n")), &ack); err != nil {
			t.Fatal(err)
		}
		if !ack.OK {
			t.Fatal("decoded ACK is not OK")
		}
	})

	t.Run("malformed", func(t *testing.T) {
		var ack Ack
		if err := Decode(Scanner(strings.NewReader("{\n")), &ack); err == nil {
			t.Fatal("expected malformed JSON error")
		}
	})

	t.Run("empty", func(t *testing.T) {
		var ack Ack
		if err := Decode(Scanner(strings.NewReader("")), &ack); !errors.Is(err, io.EOF) {
			t.Fatalf("got %v, want EOF", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		var ack Ack
		err := Decode(Scanner(strings.NewReader(strings.Repeat("x", MaxFrameSize+1)+"\n")), &ack)
		if err == nil {
			t.Fatal("expected oversized frame error")
		}
	})
}
