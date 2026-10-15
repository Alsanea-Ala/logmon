package ci

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// lefthook is the subset of lefthook.yml this package inspects.
type lefthook struct {
	PreCommit struct {
		Commands map[string]struct {
			Run string `yaml:"run"`
		} `yaml:"commands"`
	} `yaml:"pre-commit"`
}

func loadLefthook(t *testing.T) lefthook {
	t.Helper()
	raw, err := os.ReadFile("../../lefthook.yml")
	if err != nil {
		t.Fatalf("read lefthook.yml: %v", err)
	}
	var l lefthook
	if err := yaml.Unmarshal(raw, &l); err != nil {
		t.Fatalf("parse lefthook.yml: %v", err)
	}
	return l
}

// TestHookLintsWithTheGoVersionGoModRequires keeps the pre-commit lint pinned to
// the toolchain the module declares.
//
// The lint is only runnable on that version. golangci-lint v2.12 bundles
// honnef.co/go/tools v0.7.0, whose buildir walks the stdlib source in GOROOT and
// panics on AST nodes newer Go has introduced. On a machine with a newer Go
// than go.mod requires — and GOTOOLCHAIN=auto uses the newer one, because it is
// only a minimum — `make lint` dies with "interface conversion: interface {} is
// nil, not *buildir.IR" instead of reporting anything.
//
// The panic looks like a broken build. It is not: it is a version mismatch, and
// it took three stacked failures to name. The pin turns that into a version
// comparison this test makes instead.
//
// The value must equal go.mod's, not a literal, because CI installs go.mod's
// version through `go-version-file`. A pin that drifted from go.mod would lint a
// toolchain nothing else in the project ever sees, and the gate would pass while
// describing a configuration that does not exist anywhere.
func TestHookLintsWithTheGoVersionGoModRequires(t *testing.T) {
	l := loadLefthook(t)

	cmd, ok := l.PreCommit.Commands["lint"]
	if !ok {
		t.Fatal("lefthook.yml has no pre-commit lint command")
	}
	run := cmd.Run

	const marker = "GOTOOLCHAIN=go"
	at := strings.Index(run, marker)
	if at < 0 {
		t.Fatalf("the lint hook pins no GOTOOLCHAIN: %q\n"+
			"see the comment on this test for what a newer toolchain does to it", run)
	}
	pinned := strings.TrimPrefix(run[at+len(marker):], "")
	if end := strings.IndexAny(pinned, " \t\""); end >= 0 {
		pinned = pinned[:end]
	}

	raw, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	want := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		if rest, found := strings.CutPrefix(line, "go "); found {
			want = strings.TrimSpace(rest)
			break
		}
	}
	if want == "" {
		t.Fatal("go.mod declares no go directive")
	}

	if pinned != want {
		t.Errorf("the lint hook pins GOTOOLCHAIN=go%s, go.mod requires go %s\n"+
			"CI installs go.mod's version, so the hook must lint that one too", pinned, want)
	}
}
