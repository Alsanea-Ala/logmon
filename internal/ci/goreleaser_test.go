package ci

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

const goreleaserFile = "../../.goreleaser.yaml"

// goreleaserConfig is the subset of .goreleaser.yaml this package inspects.
// NFPMs is a list of untyped maps rather than a struct on purpose: a struct
// would drop any key GoReleaser does not know about, and the whole point here
// is to see a key GoReleaser does not know about.
type goreleaserConfig struct {
	NFPMs []map[string]any `yaml:"nfpms"`
}

func loadGoreleaser(t *testing.T) goreleaserConfig {
	t.Helper()
	raw, err := os.ReadFile(goreleaserFile)
	if err != nil {
		t.Fatalf("read %s: %v", goreleaserFile, err)
	}
	var c goreleaserConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", goreleaserFile, err)
	}
	return c
}

// TestNFPMDoesNotUseTheV1FilenameField catches a field name that only differs
// from the real one by a prefix.
//
// GoReleaser v2 renamed nfpms' `name_template` to `file_name_template`. Unlike
// most config mistakes this is not a warning and not a bad filename: v2 fails
// to unmarshal the file and the release dies before building anything. The tag
// still gets pushed, so the outcome is a tag that names a release which does
// not exist. That is exactly what v0.2.0 did — the tag is on the remote and
// there is no GitHub release behind it.
//
// The cost of that class of bug is one release, discovered by whoever tries to
// install the next one, and the fix is unpublishing a tag. Both are worse than
// a failed CI run on a pull request, which is where this now happens instead.
//
// Note that `archives[].name_template` and `checksum.name_template` are still
// correct in v2. Only nfpm was renamed, so this test looks at nfpm only and
// renaming the others would break them.
func TestNFPMDoesNotUseTheV1FilenameField(t *testing.T) {
	c := loadGoreleaser(t)
	if len(c.NFPMs) == 0 {
		t.Fatalf("%s declares no nfpms entries, so this would pass by checking nothing", goreleaserFile)
	}

	for i, entry := range c.NFPMs {
		id, _ := entry["id"].(string)
		if _, wrong := entry["name_template"]; wrong {
			t.Errorf("nfpms[%d] (id %q) sets `name_template`, which GoReleaser v2 removed.\n"+
				"Use `file_name_template`. An unknown key is a hard unmarshal error: the\n"+
				"release fails at config parse, no binary is built, and the tag names a\n"+
				"release that does not exist. v0.2.0 shipped this way.", i, id)
			continue
		}
		if _, ok := entry["file_name_template"]; !ok {
			t.Logf("nfpms[%d] (id %q) sets no filename template at all; GoReleaser's "+
				"default applies. That is allowed, so this is a note, not a failure.", i, id)
		}
	}
}
