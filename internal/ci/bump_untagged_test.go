package ci

import (
	"strings"
	"testing"
)

// TestBumpScriptCountsFeatsBeforeAnUntaggedBumpCommit covers the case none of
// the other tests build: a `chore: vX.Y.Z` commit whose tag never landed.
//
// Every other test in this file tags its bump commit, because a completed
// release leaves one. That is the happy path, and it is why the bug this
// describes survived three releases.
//
// The range was anchored on the most recent `chore: vX.Y.Z` subject, while the
// version base was anchored on `git describe --tags`. A release that gets its
// bump commit pushed and then fails afterwards leaves the first anchor ahead of
// the second, and the two disagree:
//
//	boundary  chore: v0.2.0 commit   range = that commit^..HEAD
//	base      git describe          v0.1.1
//
// Every feat sits before the boundary, so the range holds one fix and the bump
// commit. Patch. The twelve features shipped as v0.1.2 while the changelog, built
// from a tag range, listed all twelve under the same version.
//
// The anchor has to be a tag, because a tag is the only evidence a release
// exists. A bump commit is a decision to release. The repository under test has
// both: `chore: v0.2.0` on main with no v0.2.0 tag anywhere, left by the run
// whose publish job died on an invalid nfpm field.
//
// A tag is also the right anchor because it cannot drift from the version base.
// Anchoring both on the same lookup makes disagreement impossible by
// construction rather than by care.
func TestBumpScriptCountsFeatsBeforeAnUntaggedBumpCommit(t *testing.T) {
	tests := []struct {
		name     string
		subjects []string
		tagIndex int // rev for the completed release's tag, -1 for none
		tagName  string
		want     string
	}{
		{
			// The shape that actually happened. v0.1.0 shipped; the next
			// attempt at v0.2.0 committed its bump and then failed to publish.
			name: "an untagged v0.2.0 bump does not hide the feats before it",
			subjects: []string{
				"feat: initial import",
				"chore: v0.1.0",
				"feat: add a filter",
				"feat: add an ingest graph",
				"chore: v0.2.0",
				"fix: correct a field name",
			},
			tagIndex: -4,
			tagName:  "v0.1.0",
			want:     "v0.2.0",
		},
		{
			// Same, but the failed bump is the most recent commit. The feats
			// are still unreleased, so the retry must still reach v0.2.0
			// rather than collide with it or step over it.
			name: "an untagged bump at HEAD does not hide the feats before it",
			subjects: []string{
				"feat: initial import",
				"chore: v0.1.0",
				"feat: add a filter",
				"chore: v0.2.0",
			},
			tagIndex: -3,
			tagName:  "v0.1.0",
			want:     "v0.2.0",
		},
		{
			// Two failed attempts. The boundary used to land on the newest,
			// which is the same failure repeated.
			name: "two untagged bumps still reach the version the feats imply",
			subjects: []string{
				"feat: initial import",
				"chore: v0.1.0",
				"feat: add a filter",
				"chore: v0.2.0",
				"fix: correct a field name",
				"chore: v0.2.1",
			},
			tagIndex: -5,
			tagName:  "v0.1.0",
			want:     "v0.2.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t, tc.subjects...)
			if tc.tagName != "" {
				r.tag(tc.tagName, revAt(t, tc.tagIndex))
			}

			out, outputs, err := runBumpScript(t, r)
			if err != nil {
				t.Fatalf("script exited %v\n%s", err, out)
			}
			want := "version=" + tc.want
			if got := strings.TrimSpace(outputs); got != want {
				t.Errorf("published %q, want %q\n"+
					"An untagged bump commit means a release was attempted and did not land.\n"+
					"Its version is still available, and the features before it are still\n"+
					"unreleased.\nscript output:\n%s", got, want, out)
			}
		})
	}
}

// TestBumpScriptIgnoresBumpCommitsAfterTheLastTag is the other half, and the
// one that decides whether the fix is honest or merely convenient.
//
// A bump commit sitting after the last tag must not become a release boundary,
// but it also must not cause a release on its own. Its subject is `chore:`, so
// it contributes nothing releasable, and the correct answer is to skip the run
// rather than invent a version from a range that holds only release mechanics.
func TestBumpScriptIgnoresBumpCommitsAfterTheLastTag(t *testing.T) {
	r := newRepo(t,
		"feat: initial import",
		"chore: v0.1.0",
		"docs: explain the deployment",
	)
	r.tag("v0.1.0", revAt(t, -2))

	out, outputs, err := runBumpScript(t, r)
	if err != nil {
		t.Errorf("script exited %v on a docs-only range; want a clean exit\n%s", err, out)
	}
	if strings.Contains(outputs, "version=") {
		t.Errorf("script published a version for a docs-only range: %q\n%s", outputs, out)
	}
}

// revAt resolves a negative offset like HEAD~2 to a revision the tag helper
// accepts, so the table above can name a position without counting commits.
func revAt(t *testing.T, offset int) string {
	t.Helper()
	if offset > 0 {
		t.Fatalf("revAt takes a negative offset, got %d", offset)
	}
	return "HEAD" + strings.Repeat("~", -offset)
}
