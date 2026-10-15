package server

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// GUI configuration.
//
// Read from a YAML file at startup and re-read on `r`. Everything has a
// default, so the file is optional and a partial file is valid: absent fields
// keep their default rather than becoming zero values.
//
// Parsing is strict. An unknown key is an error rather than being ignored,
// because a mistyped key that silently does nothing is the failure mode that
// costs an afternoon.
// guiFile is the on-disk shape. The appearance settings sit under a `gui` key
// so the file can grow alongside server.yaml rather than being a flat grab bag.
type guiFile struct {
	GUI guiConfig `yaml:"gui"`
}

type guiConfig struct {
	Border         string   `yaml:"border"`
	SidePanelWidth float64  `yaml:"sidePanelWidth"`
	Graph          *bool    `yaml:"graph"`
	Wrap           *bool    `yaml:"wrap"`
	Mouse          *bool    `yaml:"mouse"`
	Theme          guiTheme `yaml:"theme"`
}

// guiTheme maps onto palette one field at a time. Every field is optional and
// falls back to defaultPalette, so a theme that sets two colours is valid.
type guiTheme struct {
	BorderActive   string `yaml:"borderActive"`
	BorderInactive string `yaml:"borderInactive"`
	SelectedBg     string `yaml:"selectedBg"`
	OptionsText    string `yaml:"optionsText"`
	Title          string `yaml:"title"`
	Muted          string `yaml:"muted"`
	Accent         string `yaml:"accent"`
	Ok             string `yaml:"ok"`
	Warn           string `yaml:"warn"`
	Err            string `yaml:"err"`
	SelectedFg     string `yaml:"selectedFg"`
}

// Sidebar width bounds. A ratio outside this range produces a panel too narrow
// to show an agent ID or too wide to leave a usable log view, so it is clamped
// rather than rejected: a layout number is not a correctness question the way a
// colour name is.
const (
	minSideRatio     = 0.20
	maxSideRatio     = 0.60
	defaultSideRatio = 1.0 / 3.0
)

const defaultBorderName = "rounded"

// defaultGUIConfig is what an absent file, or an absent field, means.
func defaultGUIConfig() guiConfig {
	on, off := true, false
	return guiConfig{
		Border:         defaultBorderName,
		SidePanelWidth: defaultSideRatio,
		Graph:          &on,
		Wrap:           &off,
		// Off, and deliberately so. Terminal mouse reporting takes over text
		// selection: with it enabled, dragging does not select, and copying a
		// log line is the most common thing anyone does with one. There is no
		// key to turn this on because Bubble Tea negotiates the reporting mode
		// with the terminal at startup and cannot renegotiate from inside the
		// program.
		Mouse: &off,
	}
}

// loadGUIConfig reads path and fills in defaults.
//
// A missing file is not an error: the common case is no file at all. An
// unreadable or invalid one is, and the caller falls back to defaults and says
// so rather than starting with a half-applied look.
func loadGUIConfig(path string) (guiConfig, error) {
	cfg := defaultGUIConfig()
	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return defaultGUIConfig(), fmt.Errorf("gui config: %w", err)
	}
	defer file.Close()

	// Decoding over the populated defaults is what makes a partial file valid:
	// a file that sets only a border leaves every other field at its default.
	var parsed guiFile
	parsed.GUI = cfg
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&parsed); err != nil && !errors.Is(err, io.EOF) {
		return defaultGUIConfig(), fmt.Errorf("gui config %s: %w", path, err)
	}
	cfg = parsed.GUI

	if err := cfg.validate(); err != nil {
		return defaultGUIConfig(), fmt.Errorf("gui config %s: %w", path, err)
	}
	return cfg, nil
}

// validate rejects names that could only be a mistake.
//
// Everything here could be substituted silently, and a config that quietly
// ignores a bad colour is a config nobody can debug.
func (c guiConfig) validate() error {
	if _, err := borderByName(c.Border); err != nil {
		return err
	}
	// Applied to a throwaway palette: validate the names, do not keep the result.
	probe := defaultPalette
	return c.Theme.applyTo(&probe)
}

// sideRatio is the clamped sidebar share of the terminal width.
func (c guiConfig) sideRatio() float64 {
	if c.SidePanelWidth <= 0 {
		return defaultSideRatio
	}
	return min(max(c.SidePanelWidth, minSideRatio), maxSideRatio)
}

// applyTo writes the theme onto a palette, leaving unset fields alone.
func (t guiTheme) applyTo(p *palette) error {
	fields := []struct {
		key   string
		value string
		dest  *lipgloss.TerminalColor
	}{
		{"borderActive", t.BorderActive, &p.BorderActive},
		{"borderInactive", t.BorderInactive, &p.BorderInactive},
		{"selectedBg", t.SelectedBg, &p.SelectedBg},
		{"optionsText", t.OptionsText, &p.OptionsText},
		{"title", t.Title, &p.Title},
		{"muted", t.Muted, &p.Muted},
		{"accent", t.Accent, &p.Accent},
		{"ok", t.Ok, &p.Ok},
		{"warn", t.Warn, &p.Warn},
		{"err", t.Err, &p.Err},
		{"selectedFg", t.SelectedFg, &p.SelectedFg},
	}
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		color, err := parseColor(field.value)
		if err != nil {
			return fmt.Errorf("theme.%s: %w", field.key, err)
		}
		*field.dest = color
	}
	return nil
}

// borderByName resolves the border style.
//
// "hidden" is the zero Border, which panel() already reads as frameless: no
// edges, no padding, and no title bar, because a title with no border to sit in
// renders as the first row of the body.
func borderByName(name string) (lipgloss.Border, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "rounded":
		return lipgloss.RoundedBorder(), nil
	case "single", "normal":
		return lipgloss.NormalBorder(), nil
	case "double":
		return lipgloss.DoubleBorder(), nil
	case "hidden", "none":
		return lipgloss.Border{}, nil
	default:
		return lipgloss.Border{}, fmt.Errorf("unknown border %q: want rounded, single, double or hidden", name)
	}
}

// namedColors are the sixteen ANSI names, mapped to their 256-colour indices.
//
// Indices 0-15 are the ANSI colours themselves, so a name resolves to exactly
// the colour it names on any terminal that speaks the xterm palette. Anything
// outside this set is a typo rather than a preference.
var namedColors = map[string]string{
	"black": "0", "red": "1", "green": "2", "yellow": "3",
	"blue": "4", "magenta": "5", "cyan": "6", "white": "7",
	"brightblack": "8", "brightred": "9", "brightgreen": "10", "brightyellow": "11",
	"brightblue": "12", "brightmagenta": "13", "brightcyan": "14", "brightwhite": "15",
}

// parseColor accepts a name, a 256-colour index, or #rrggbb.
//
// The numeric form exists because the palette this replaced is expressed as
// 256-colour indices, and forcing everyone through sixteen names to express
// "the 214 I had before" would make the config a downgrade.
func parseColor(value string) (lipgloss.TerminalColor, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("empty colour")
	}

	if index, ok := namedColors[strings.ToLower(trimmed)]; ok {
		return lipgloss.Color(index), nil
	}

	if strings.HasPrefix(trimmed, "#") {
		if len(trimmed) != 7 {
			return nil, fmt.Errorf("colour %q: want #rrggbb", value)
		}
		if _, err := strconv.ParseUint(trimmed[1:], 16, 32); err != nil {
			return nil, fmt.Errorf("colour %q: %w", value, err)
		}
		return lipgloss.Color(trimmed), nil
	}

	index, err := strconv.Atoi(trimmed)
	if err != nil {
		return nil, fmt.Errorf("unknown colour %q: want a name, 0-255, or #rrggbb", value)
	}
	if index < 0 || index > 255 {
		return nil, fmt.Errorf("colour index %d out of range 0-255", index)
	}
	return lipgloss.Color(strconv.Itoa(index)), nil
}

// defaultGUIConfigPath is where the config lives when no path is given.
//
// XDG rather than a dotfile in the home directory, which is the convention on
// Linux and is what an operator would expect from a tool that has no other
// opinion about layout.
func defaultGUIConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return home + string(os.PathSeparator) + ".config" + string(os.PathSeparator) +
		"logmon" + string(os.PathSeparator) + "config.yml"
}

// loadGUI applies path to the model, falling back to defaults on error.
//
// The error is kept rather than logged: the TUI is the only place the operator
// is, and a config that did not apply is worth a visible line.
func (m *model) loadGUI(path string) {
	m.guiConfigPath = path
	cfg, err := loadGUIConfig(path)
	if err != nil {
		m.guiErr = err
		m.applyGUIConfig(defaultGUIConfig())
		return
	}
	m.guiErr = nil
	m.applyGUIConfig(cfg)
}

// applyGUIConfig takes the file as the source of truth, including the toggles.
//
// Re-reading resets `g` and `w` to whatever the file says. That is the price
// of having one rule rather than two: the file wins, always, and the operator
// who wants the graph back presses the key again.
func (m *model) applyGUIConfig(cfg guiConfig) {
	border, err := borderByName(cfg.Border)
	if err != nil {
		border = lipgloss.RoundedBorder()
	}
	m.borderStyle = border
	m.sidePanelRatio = cfg.sideRatio()

	if cfg.Graph != nil {
		m.graphEnabled = *cfg.Graph
	}
	if cfg.Wrap != nil {
		m.wrap = *cfg.Wrap
	}
	if cfg.Mouse != nil {
		m.mouseEnabled = *cfg.Mouse
	}

	// Theme errors are caught by validate before here, so this cannot fail on a
	// config that reached apply.
	_ = cfg.Theme.applyTo(&m.p)
}

// framed reports whether panels draw a border at all.
func (m *model) framed() bool {
	return m.borderStyle.TopLeft != "" || m.borderStyle.Left != ""
}

// bodyWidth is the text column inside a panel of the given outer width.
//
// Frameless panels spend no columns on a border or padding, so charging them
// textWidth would leave four columns empty on every row.
func (m *model) bodyWidth(width int) int {
	chrome := textWidth
	if !m.framed() {
		chrome = 0
	}
	return max(1, width-chrome)
}

// panelChromeV is the vertical room a border costs.
//
// Zero for a frameless panel, which frees two rows in every budget that used to
// assume them: the graph threshold and the log panel's row count.
func (m *model) panelChromeV() int {
	if !m.framed() {
		return 0
	}
	return chromeHeight
}

// reloadGUI re-reads the config file, keeping the current settings when it is
// unreadable or invalid so a bad edit does not blank the panel.
func (m *model) reloadGUI() {
	if m.guiConfigPath == "" {
		return
	}
	cfg, err := loadGUIConfig(m.guiConfigPath)
	if err != nil {
		m.guiErr = err
		return
	}
	m.guiErr = nil
	m.applyGUIConfig(cfg)
}
