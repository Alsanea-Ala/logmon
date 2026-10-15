package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a throwaway git repository plus a helper for running more git
// commands against it.
type repo struct {
	dir string
	run func(args ...string)
	tag func(name, rev string)
}

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
}

// newRepo builds a repository whose history is the given subjects, oldest
// first, with the last one checked out.
func newRepo(t *testing.T, subjects ...string) *repo {
	t.Helper()
	if len(subjects) == 0 {
		t.Fatal("newRepo needs at least one commit")
	}
	r := &repo{dir: t.TempDir()}

	r.run = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = r.dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	r.tag = func(name, rev string) {
		t.Helper()
		cmd := exec.Command("git", "tag", "-a", name, "-m", "Release "+name, rev)
		cmd.Dir = r.dir
		cmd.Env = gitEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git tag %s %s: %v\n%s", name, rev, err, out)
		}
	}

	r.run("init", "-q", "-b", "main")
	for _, subject := range subjects {
		if err := os.WriteFile(filepath.Join(r.dir, "f"), []byte(subject), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		r.run("add", "f")
		r.run("commit", "-q", "-m", subject)
	}
	return r
}

// danglingTag creates a tag on a commit that is not reachable from HEAD, so it
// exists for `git tag -l` but is invisible to `git describe`. That isolates the
// version-computation base from the tag the script is meant to collide with.
func (r *repo) danglingTag(t *testing.T, name string) {
	t.Helper()
	r.run("commit", "-q", "--allow-empty", "-m", "temporary")
	r.tag(name, "HEAD")
	r.run("reset", "-q", "--hard", "HEAD~1")
}

// runBumpScript executes the real bump script from the workflow against this
// repository, returning its combined output and the contents of GITHUB_OUTPUT.
//
// The script is read out of the YAML rather than duplicated here, so a test can
// never pass against a copy that has drifted from the workflow.
func runBumpScript(t *testing.T, r *repo) (output string, outputs string, err error) {
	t.Helper()

	script := filepath.Join(r.dir, "bump.sh")
	if err := os.WriteFile(script, []byte(bumpScript(t)), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	outFile := filepath.Join(r.dir, "outputs.txt")
	if err := os.WriteFile(outFile, nil, 0o600); err != nil {
		t.Fatalf("write outputs: %v", err)
	}

	cmd := exec.Command("bash", script)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+outFile)

	out, err := cmd.CombinedOutput()
	raw, readErr := os.ReadFile(outFile)
	if readErr != nil {
		t.Fatalf("read outputs: %v", readErr)
	}
	return string(out), string(raw), err
}

// TestBumpScriptSkipsUnreleaseableRange is the regression test for the bug that
// shipped in release-tag run #5.
//
// The job classified a chore-only range as "no release", then printed
// `exit 0` instead of running it, fell through, and recomputed v0.1.0 from the
// very tag that range had produced. It failed three steps later with
// `fatal: tag 'v0.1.0' already exists`, naming neither the cause nor the range
// responsible.
//
// A non-releasable range must exit 0 and publish no version at all.
func TestBumpScriptSkipsUnreleaseableRange(t *testing.T) {
	tests := []struct {
		name     string
		subjects []string
		released bool // tag the release commit, as a completed release would
	}{
		{
			name:     "chore and ci after a release",
			subjects: []string{"feat: initial import", "chore: v0.1.0", "ci: adjust a workflow"},
			released: true,
		},
		{
			name:     "docs after a release",
			subjects: []string{"feat: initial import", "chore: v0.1.0", "docs: rewrite the readme"},
			released: true,
		},
		{
			name:     "no release ever, and nothing releasable",
			subjects: []string{"chore: set up the repository", "ci: add a workflow"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, tc.subjects...)
			if tc.released {
				r.tag("v0.1.0", "HEAD~1")
			}

			out, outputs, err := runBumpScript(t, r)

			if err != nil {
				t.Errorf("script exited %v on a non-releasable range; want a clean exit\n%s",
					err, out)
			}
			if strings.Contains(outputs, "version=") {
				t.Errorf("script published a version on a non-releasable range: %q\n%s",
					outputs, out)
			}
			if !strings.Contains(out, "no release-worthy commit") {
				t.Errorf("script did not report skipping the release:\n%s", out)
			}
		})
	}
}

// TestBumpScriptVersionsReleasableRange proves the guard did not become a
// blanket "never release": a feat or fix in the range must still version.
func TestBumpScriptVersionsReleasableRange(t *testing.T) {
	tests := []struct {
		name     string
		subjects []string
		tagged   string // tag on subjects[1], as a completed release would leave
		want     string
	}{
		{
			name:     "feat after a patch release is a minor",
			subjects: []string{"feat: initial import", "chore: v0.1.0", "feat: add a filter"},
			tagged:   "v0.1.0",
			want:     "v0.2.0",
		},
		{
			name:     "fix after a minor release is a patch",
			subjects: []string{"feat: initial import", "chore: v0.2.0", "fix: stop duplicating records"},
			tagged:   "v0.2.0",
			want:     "v0.2.1",
		},
		{
			name:     "breaking change is a major",
			subjects: []string{"feat: initial import", "chore: v0.2.0", "feat!: drop the v1 protocol"},
			tagged:   "v0.2.0",
			want:     "v1.0.0",
		},
		{
			name:     "first release is v0.1.0 regardless of history",
			subjects: []string{"feat: initial import", "feat: add a server"},
			want:     "v0.1.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, tc.subjects...)
			if tc.tagged != "" {
				r.tag(tc.tagged, "HEAD~1")
			}

			out, outputs, err := runBumpScript(t, r)
			if err != nil {
				t.Fatalf("script exited %v on a releasable range\n%s", err, out)
			}
			want := "version=" + tc.want
			if got := strings.TrimSpace(outputs); got != want {
				t.Errorf("published %q, want %q\nscript output:\n%s", got, want, out)
			}
		})
	}
}

// TestBumpScriptRefusesExistingTag covers the other half: even on a releasable
// range, a version that is already tagged must fail loudly and name the cause,
// rather than emit a version that dies three steps later as
// `fatal: tag already exists`.
func TestBumpScriptRefusesExistingTag(t *testing.T) {
	r := newRepo(t,
		"feat: initial import",
		"chore: v0.1.0",
		"feat: add a filter",
	)
	r.tag("v0.1.0", "HEAD~1")
	// The range above computes to v0.2.0. Park that tag on an unreachable
	// commit so it is visible to `git tag -l` but not to `git describe`,
	// which keeps the computed base at v0.1.0 and the collision exact.
	r.danglingTag(t, "v0.2.0")

	out, outputs, err := runBumpScript(t, r)
	if err == nil {
		t.Errorf("script accepted a version that is already tagged\n%s", out)
	}
	if strings.Contains(outputs, "version=") {
		t.Errorf("script published a version that is already tagged: %q\n%s", outputs, out)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("failure did not name the cause; want a message naming the existing tag\n%s", out)
	}
}
