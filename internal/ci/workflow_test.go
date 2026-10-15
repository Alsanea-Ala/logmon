// Package ci holds no runtime code. It exists so the release pipeline's shell,
// which is otherwise only ever exercised by GitHub, gets the same scrutiny as
// the Go code.
//
// Three defects in release-tag.yml shipped in a row, and all three were silent:
// the job went green, or failed somewhere other than the cause. None of them
// were syntax errors, so `bash -n` alone would have passed every one.
package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const workflowsDir = "../../.github/workflows"

// workflow is the subset of a workflow this package inspects.
type workflow struct {
	Jobs map[string]struct {
		Steps []struct {
			ID   string `yaml:"id"`
			Name string `yaml:"name"`
			Uses string `yaml:"uses"`
			If   string `yaml:"if"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workflowsDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var w workflow
	if err := yaml.Unmarshal(raw, &w); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return w
}

func TestWorkflowScriptsAreValidBash(t *testing.T) {
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		t.Fatalf("read %s: %v", workflowsDir, err)
	}
	if len(entries) == 0 {
		t.Fatal("no workflows found")
	}

	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		w := loadWorkflow(t, entry.Name())
		for job, def := range w.Jobs {
			for _, step := range def.Steps {
				if step.Run == "" {
					continue
				}
				label := entry.Name() + "/" + job + "/" + step.Name
				cmd := exec.Command("bash", "-n")
				cmd.Stdin = strings.NewReader(step.Run)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s: bash -n: %v\n%s", label, err, out)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("checked no run scripts at all")
	}
	t.Logf("checked %d step scripts", checked)
}

// TestReleaseStepsAreGuarded pins the structural half of the fix.
//
// The bump step ends a non-releasable range with `exit 0`. That skips only
// itself: GitHub runs the remaining steps regardless, and they would receive an
// empty version. So every step after the bump step has to be gated on its
// output.
//
// This went wrong before in a way no other check caught: the guard printed the
// string "exit 0" instead of executing it, so the job fell through, recomputed a
// version for the range it had just rejected, and tried to ship a tag that
// already existed. See TestBumpScriptSkipsUnreleaseableRange for that half.
func TestReleaseStepsAreGuarded(t *testing.T) {
	w := loadWorkflow(t, "release-tag.yml")

	var steps []struct {
		ID   string
		Name string
		If   string
	}
	for _, def := range w.Jobs {
		for _, step := range def.Steps {
			steps = append(steps, struct {
				ID   string
				Name string
				If   string
			}{step.ID, step.Name, step.If})
		}
	}

	bumpAt := -1
	for i, s := range steps {
		if s.ID == "bump" {
			bumpAt = i
			break
		}
	}
	if bumpAt < 0 {
		t.Fatal("no step has id: bump, so nothing is gated on the version")
	}

	for _, s := range steps[bumpAt+1:] {
		if s.If == "" {
			t.Errorf("step %q runs after the bump step with no if: guard; "+
				"it would execute against an empty version on a "+
				"non-releasable range", s.Name)
			continue
		}
		if !strings.Contains(s.If, "steps.bump.outputs") {
			t.Errorf("step %q has if: %q, which does not reference the bump "+
				"step's output", s.Name, s.If)
		}
	}
}

func bumpScript(t *testing.T) string {
	t.Helper()
	w := loadWorkflow(t, "release-tag.yml")
	for _, def := range w.Jobs {
		for _, step := range def.Steps {
			if step.ID == "bump" {
				return step.Run
			}
		}
	}
	t.Fatal("no step has id: bump")
	return ""
}
