package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	return path
}

// TestGUIConfigRejectsUnknownKeys is the strict-parsing rule.
//
// A mistyped key that is silently ignored is the failure that costs an
// afternoon: the operator has written a colour name, the server starts, and
// nothing changes with no message about why.
func TestGUIConfigRejectsUnknownKeys(t *testing.T) {
	path := writeConfig(t, "gui:\n  notARealKey: true\n")
	if _, err := loadGUIConfig(path); err == nil {
		t.Fatal("an unknown key was accepted")
	} else if !strings.Contains(err.Error(), "notARealKey") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
}

// TestGUIConfigDefaultsApplyToOmittedFields is what makes a partial file valid.
func TestGUIConfigDefaultsApplyToOmittedFields(t *testing.T) {
	cfg, err := loadGUIConfig(writeConfig(t, "gui:\n  border: double\n"))
	if err != nil {
		t.Fatalf("a partial config was rejected: %v", err)
	}

	if cfg.Border != "double" {
		t.Errorf("border is %q, want %q", cfg.Border, "double")
	}
	if cfg.sideRatio() != defaultSideRatio {
		t.Errorf("sidePanelWidth defaulted to %v, want %v", cfg.sideRatio(), defaultSideRatio)
	}
	if cfg.Graph == nil || !*cfg.Graph {
		t.Error("graph did not default to on")
	}
	if cfg.Wrap == nil || *cfg.Wrap {
		t.Error("wrap did not default to off")
	}
	// Unset theme fields leave the built-in palette alone.
	probe := defaultPalette
	if err := cfg.Theme.applyTo(&probe); err != nil {
		t.Fatalf("the empty theme did not apply: %v", err)
	}
	if probe != defaultPalette {
		t.Error("an empty theme changed the default palette")
	}
}

// TestGUIConfigMissingFileIsNotAnError covers the common case.
func TestGUIConfigMissingFileIsNotAnError(t *testing.T) {
	cfg, err := loadGUIConfig(filepath.Join(t.TempDir(), "absent.yml"))
	if err != nil {
		t.Fatalf("a missing config is an error: %v", err)
	}
	if cfg.Border != defaultBorderName {
		t.Errorf("a missing config did not fall back to defaults: %q", cfg.Border)
	}

	// An empty path disables it entirely.
	if _, err := loadGUIConfig(""); err != nil {
		t.Errorf("an empty path is an error: %v", err)
	}
}

// TestGUIConfigInvalidBorderIsRejectedNotSubstituted is the difference between
// a loud failure and a silent one.
func TestGUIConfigInvalidBorderIsRejectedNotSubstituted(t *testing.T) {
	cfg, err := loadGUIConfig(writeConfig(t, "gui:\n  border: squiggly\n"))
	if err == nil {
		t.Fatal("an unknown border name was accepted")
	}
	if !strings.Contains(err.Error(), "squiggly") {
		t.Errorf("the error does not name the bad border: %v", err)
	}
	// The returned config is the defaults, not the defaults-with-a-guess.
	if cfg.Border != defaultBorderName {
		t.Errorf("the fallback border is %q, want %q", cfg.Border, defaultBorderName)
	}
}

// TestGUIConfigInvalidColourIsRejected covers the same rule for the theme.
func TestGUIConfigInvalidColourIsRejected(t *testing.T) {
	cases := []string{
		"gui:\n  theme:\n    borderActive: chartreuse\n",
		"gui:\n  theme:\n    err: 999\n",
		"gui:\n  theme:\n    accent: '#12'\n",
		"gui:\n  theme:\n    accent: 'not-a-colour'\n",
	}
	for _, body := range cases {
		if _, err := loadGUIConfig(writeConfig(t, body)); err == nil {
			t.Errorf("accepted an invalid colour:\n%s", body)
		}
	}
}

// TestGUIConfigAcceptsEveryColourForm is why the numeric form exists: the
// palette this replaced is expressed as 256-colour indices and a config that
// cannot express them is a downgrade.
func TestGUIConfigAcceptsEveryColourForm(t *testing.T) {
	for _, value := range []string{
		"green", "brightred", "46", "0", "255", "#ff8800", "#FF8800",
	} {
		if _, err := parseColor(value); err != nil {
			t.Errorf("parseColor(%q) failed: %v", value, err)
		}
	}
	for _, value := range []string{"", "-1", "256", "chartreuse", "#gg8800", "#1234567"} {
		if _, err := parseColor(value); err == nil {
			t.Errorf("parseColor(%q) was accepted", value)
		}
	}
}

// TestGUISideRatioIsClampedNotRejected: a layout number is not a correctness
// question the way a colour name is, so an absurd ratio is clamped.
func TestGUISideRatioIsClampedNotRejected(t *testing.T) {
	cases := map[float64]float64{
		0.05: minSideRatio,
		0.95: maxSideRatio,
		0.33: 0.33,
		0:    defaultSideRatio,
		-1:   defaultSideRatio,
	}
	for in, want := range cases {
		cfg := defaultGUIConfig()
		cfg.SidePanelWidth = in
		if got := cfg.sideRatio(); got != want {
			t.Errorf("sidePanelWidth %v clamped to %v, want %v", in, got, want)
		}
	}
}

// TestGUIHiddenBorderFreesTwoRows is the reason the border is configurable at
// all: a frameless panel has two more rows to give, and every row budget that
// hard-coded chromeHeight was leaving them unused.
func TestGUIHiddenBorderFreesTwoRows(t *testing.T) {
	framed := testModel(t, 140, 40)
	if !framed.framed() {
		t.Fatal("the default is not a framed panel")
	}
	if got := framed.panelChromeV(); got != chromeHeight {
		t.Errorf("a framed panel costs %d rows, want %d", got, chromeHeight)
	}

	hidden := testModel(t, 140, 40)
	cfg := defaultGUIConfig()
	cfg.Border = "hidden"
	hidden.applyGUIConfig(cfg)

	if hidden.framed() {
		t.Fatal("the hidden border still frames")
	}
	if got := hidden.panelChromeV(); got != 0 {
		t.Errorf("a frameless panel costs %d rows, want 0", got)
	}
	// The graph threshold is expressed in real rows, so it now admits a panel
	// that was previously two rows short of it.
	if !hidden.graphVisible(graphMinRows) {
		t.Error("the graph threshold did not account for the freed rows")
	}
	if framed.graphVisible(graphMinRows) {
		t.Error("a framed panel fits the graph at the unframed threshold")
	}
}

// TestGUIBorderChangesTheFrame checks the config reaches the renderer.
func TestGUIBorderChangesTheFrame(t *testing.T) {
	m := testModel(t, 140, 40)

	cfg := defaultGUIConfig()
	cfg.Border = "double"
	m.applyGUIConfig(cfg)
	if !strings.Contains(stripANSI(m.renderAgents(40, 8)), "╔") {
		t.Error("the double border was not applied")
	}

	cfg.Border = "hidden"
	m.applyGUIConfig(cfg)
	frame := stripANSI(m.renderAgents(40, 8))
	if strings.Contains(frame, "╭") || strings.Contains(frame, "│") {
		t.Errorf("the hidden border still drew a frame:\n%s", frame)
	}
	// No border means no title bar either: a title with no border to sit in
	// renders as the first row of the body.
	if strings.Contains(frame, "[1] Agents") {
		t.Errorf("a frameless panel still draws a title:\n%s", frame)
	}
}

// TestGUIThemeChangesTheFrame checks a colour name reaches the palette.
func TestGUIThemeChangesTheFrame(t *testing.T) {
	m := testModel(t, 140, 40)
	cfg := defaultGUIConfig()
	cfg.Theme = guiTheme{BorderActive: "red", Accent: "brightyellow"}
	m.applyGUIConfig(cfg)

	if got := colorIndex(m.p.BorderActive); got != "1" {
		t.Errorf("borderActive resolved to %q, want %q", got, "1")
	}
	if got := colorIndex(m.p.Accent); got != "11" {
		t.Errorf("accent resolved to %q, want %q", got, "11")
	}
	// Fields the file did not mention keep their built-in values.
	if got := colorIndex(m.p.Err); got != colorIndex(defaultPalette.Err) {
		t.Errorf("an unmentioned field changed from %q to %q",
			colorIndex(defaultPalette.Err), got)
	}
}

// TestGUIBadConfigFallsBackAndSays is the operator-facing half: the panel runs
// on defaults and says so, rather than starting up in the wrong colours.
func TestGUIBadConfigFallsBackAndSays(t *testing.T) {
	m := testModel(t, 140, 40)
	m.loadGUI(writeConfig(t, "gui:\n  border: squiggly\n"))

	if m.guiErr == nil {
		t.Fatal("a bad config was accepted silently")
	}
	// The panel is running on defaults.
	if m.framed() != true {
		t.Error("the fallback did not restore the default border")
	}
	if m.sidePanelRatio != defaultSideRatio {
		t.Errorf("the fallback side ratio is %v, want %v", m.sidePanelRatio, defaultSideRatio)
	}

	frame := stripANSI(m.renderMain(100, 20))
	if !strings.Contains(frame, "GUI config") {
		t.Errorf("the panel does not say the config did not apply:\n%s", frame)
	}
}

// TestGUIReloadKeepsCurrentSettingsOnABadEdit is the case that matters while
// editing: a half-saved file must not blank the panel mid-session.
func TestGUIReloadKeepsCurrentSettingsOnABadEdit(t *testing.T) {
	path := writeConfig(t, "gui:\n  border: double\n  theme:\n    accent: brightblue\n")
	m := testModel(t, 140, 40)
	m.loadGUI(path)

	if m.guiErr != nil {
		t.Fatalf("a good config was rejected: %v", m.guiErr)
	}
	if m.guiConfigPath != path {
		t.Fatalf("the config path is %q, want %q", m.guiConfigPath, path)
	}

	// Break it.
	if err := os.WriteFile(path, []byte("gui:\n  border: squiggly\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.reloadGUI()

	if m.guiErr == nil {
		t.Error("a broken config was accepted on reload")
	}
	if !strings.Contains(stripANSI(m.renderAgents(40, 8)), "╔") {
		t.Error("the good border was thrown away by a bad edit")
	}
	if colorIndex(m.p.Accent) != "12" {
		t.Errorf("the good theme was thrown away by a bad edit, accent is now %q",
			colorIndex(m.p.Accent))
	}
}

// TestGUIReloadResetsTogglesToTheFile is the one rule: the file wins.
func TestGUIReloadResetsTogglesToTheFile(t *testing.T) {
	path := writeConfig(t, "gui:\n  graph: true\n  wrap: false\n")
	m := testModel(t, 140, 40)
	m.loadGUI(path)

	// The operator turns both on, then edits the file to say otherwise.
	m.graphEnabled = false
	m.wrap = true
	m.reloadGUI()

	if !m.graphEnabled {
		t.Error("`r` did not restore graph to what the file says")
	}
	if m.wrap {
		t.Error("`r` did not restore wrap to what the file says")
	}
}

// TestGUIConfigTogglesAreAppliedAtStartup checks the two booleans are wired
// through, since they are the only fields that are not plain values.
func TestGUIConfigTogglesAreAppliedAtStartup(t *testing.T) {
	m := testModel(t, 140, 40)
	cfg := defaultGUIConfig()
	cfg.Graph, cfg.Wrap = new(bool), new(bool)
	*cfg.Graph = false
	*cfg.Wrap = true
	m.applyGUIConfig(cfg)

	if m.graphEnabled {
		t.Error("graph: false was not applied")
	}
	if !m.wrap {
		t.Error("wrap: true was not applied")
	}
}

// TestGUIReloadsOnR ties the reload to the key the operator would actually use.
func TestGUIReloadsOnR(t *testing.T) {
	path := writeConfig(t, "gui:\n  border: double\n")
	m := testModel(t, 140, 40)
	m.loadGUI(path)

	// Change the file without touching the model.
	if err := os.WriteFile(path, []byte("gui:\n  border: hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(*model)

	if cmd == nil {
		t.Error("`r` did not return a rescan command")
	}
	if m.framed() {
		t.Error("`r` did not re-read the config file")
	}
	if m.guiErr != nil {
		t.Errorf("the reload reported an error: %v", m.guiErr)
	}
}

func colorIndex(c lipgloss.TerminalColor) string {
	if v, ok := c.(lipgloss.Color); ok {
		return string(v)
	}
	return ""
}
