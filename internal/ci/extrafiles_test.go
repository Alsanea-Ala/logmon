package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseConfig is the subset of .goreleaser.yaml that decides which raw
// binaries get uploaded alongside the archives and packages.
type releaseConfig struct {
	Release struct {
		ExtraFiles []struct {
			Glob         string `yaml:"glob"`
			NameTemplate string `yaml:"name_template"`
		} `yaml:"extra_files"`
	} `yaml:"release"`
}

func loadReleaseConfig(t *testing.T) releaseConfig {
	t.Helper()
	raw, err := os.ReadFile(goreleaserFile)
	if err != nil {
		t.Fatalf("read %s: %v", goreleaserFile, err)
	}
	var c releaseConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", goreleaserFile, err)
	}
	return c
}

// fakeDist lays out dist/ the way GoReleaser actually writes it, which is the
// whole point: the binary is not at the root.
//
//	dist/agent_linux_amd64_v1/logmon-agent_linux_amd64
//	dist/agent_linux_arm64_v8.0/logmon-agent_linux_arm64
//	dist/server_linux_amd64_v1/logmon-server_linux_amd64
//	dist/logmon-agent_linux_amd64_0.1.1.tar.gz
//	dist/logmon-agent_linux_amd64_0.1.1.deb
//	...
//
// The per-target directory is named after the build id, not after `binary`.
func fakeDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, p := range []string{
		"dist/agent_linux_amd64_v1/logmon-agent_linux_amd64",
		"dist/agent_linux_arm64_v8.0/logmon-agent_linux_arm64",
		"dist/server_linux_amd64_v1/logmon-server_linux_amd64",
		"dist/server_linux_arm64_v8.0/logmon-server_linux_arm64",
		"dist/logmon-agent_linux_amd64_0.1.1.tar.gz",
		"dist/logmon-agent_linux_amd64_0.1.1.tar.zst",
		"dist/logmon-agent_linux_amd64_0.1.1.deb",
		"dist/logmon-agent_linux_amd64_0.1.1.rpm",
		"dist/logmon-agent_linux_arm64_0.1.1.tar.gz",
		"dist/logmon-server_linux_amd64_0.1.1.deb",
		"dist/logmon-server_linux_arm64_0.1.1.rpm",
		"dist/checksums.txt",
	} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", p, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// TestExtraFilesGlobsMatchOnlyRawBinaries holds the line that extra_files has
// to sit on.
//
// GoReleaser writes each raw binary into a per-target subdirectory named after
// the build id -- dist/agent_linux_amd64_v1/logmon-agent_linux_amd64 -- and
// leaves the archives and packages at the dist/ root. A glob rooted at the
// root, of the form ./dist/logmon-agent_*, therefore matches no binary at all.
// It matches the eight archives and packages built from that same binary,
// which GoReleaser already uploads on its own.
//
// The release then dies at upload with
//
//	failed to add extra_file: "./dist/logmon-agent_*": glob matches multiple files
//
// which is the visible symptom. The invisible one is worse: before that
// failure, the config was re-uploading assets that were already being
// published, and never shipped a raw binary at all. Every release through
// v0.1.2 did that. Nothing warned, because the failure only appears once a
// release actually reaches the publish step.
//
// Two properties are asserted, because either alone is satisfiable by a broken
// glob. A glob that matches nothing is as wrong as one that matches everything,
// and it fails silently -- extra_files contributes no assets and the release
// still goes green.
func TestExtraFilesGlobsMatchOnlyRawBinaries(t *testing.T) {
	c := loadReleaseConfig(t)
	if len(c.Release.ExtraFiles) == 0 {
		t.Skipf("%s declares no release.extra_files; nothing to upload raw", goreleaserFile)
	}

	dist := fakeDist(t)
	uploads := map[string]string{} // asset name -> glob that matched it

	for i, extra := range c.Release.ExtraFiles {
		pattern := strings.TrimPrefix(extra.Glob, "./")
		matches, err := filepath.Glob(filepath.Join(dist, pattern))
		if err != nil {
			t.Fatalf("nfpms[%d] glob %q is not a valid pattern: %v", i, extra.Glob, err)
		}
		if len(matches) == 0 {
			t.Errorf("extra_files[%d] glob %q matches nothing in a real dist/.\n"+
				"An extra_files glob that matches nothing uploads nothing and the release\n"+
				"still goes green. GoReleaser puts the raw binary in a per-target\n"+
				"subdirectory named after the build id, not at the dist/ root.", i, extra.Glob)
			continue
		}

		for _, m := range matches {
			base := filepath.Base(m)
			if ext := filepath.Ext(base); ext != "" {
				t.Errorf("extra_files[%d] glob %q matched %s.\n"+
					"That is an archive or a package, not a raw binary. GoReleaser already\n"+
					"uploads those, and uploading them again under the same name fails with\n"+
					"`glob matches multiple files`.", i, extra.Glob, base)
				continue
			}
			if prev, dup := uploads[base]; dup {
				t.Errorf("extra_files[%d] uploads %q, already uploaded by glob %q.\n"+
					"Two globs claiming the same asset name fail at upload.", i, base, prev)
				continue
			}
			uploads[base] = extra.Glob
		}
	}

	// The point of the feature is a raw binary per build per architecture. If
	// extra_files is present but contributes nothing useful, say so loudly
	// rather than letting a release ship without them.
	if len(c.Release.ExtraFiles) > 0 && len(uploads) == 0 {
		t.Error("extra_files is configured but resolved to no raw binary")
	}
}

// TestExtraFilesDoesNotRename catches a name_template that strips the version.
//
// The binary is already named correctly by builds.binary. A name_template on
// top of that can only overwrite that name, and extra_files exposes no
// template variable carrying the matched path -- so the best a template can do
// is remove information. It produced logmon-agent_linux_amd64 for every
// release, indistinguishable from the previous one in the asset list.
//
// Dropping the template is deliberate, not an oversight.
func TestExtraFilesDoesNotRename(t *testing.T) {
	c := loadReleaseConfig(t)

	for i, extra := range c.Release.ExtraFiles {
		if extra.NameTemplate != "" {
			t.Errorf("extra_files[%d] sets name_template %q.\n"+
				"GoReleaser already names the binary via builds.binary, and extra_files has\n"+
				"no template variable for the matched path, so this can only strip the\n"+
				"version from the name rather than add it.", i, extra.NameTemplate)
		}
	}
}
