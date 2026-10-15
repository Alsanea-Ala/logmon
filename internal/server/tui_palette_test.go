package server

import (
	"math"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Colour selection in this palette is constrained by something that is not
// obvious from reading the numbers: indices 0-15 are the sixteen ANSI colours
// and a terminal theme is free to remap all of them, while 16-255 are fixed RGB
// in the xterm palette and render identically everywhere.
//
// That matters for a foreground only as cosmetics. A border that comes out a
// different green than you expected still reads as a border. It matters for a
// background because the foreground sitting on top of it is chosen to contrast
// with a colour nobody can predict.
//
// The test below enforces the range rather than a specific value, because the
// range is the part that holds no matter what any terminal decides.

// backgroundCall finds every .Background(...) in tui.go and captures the colour
// expression passed to it.
var backgroundCall = regexp.MustCompile(`\.Background\(\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\)`)

// TestEveryBackgroundColourIsThemeIndependent is the rule that would have caught
// the selection bar.
//
// SelectedBg was "4", the ANSI blue. A terminal theme that maps blue to a light
// blue, which is common, produced a selection bar the text could not be read
// against. The value was not wrong in the source and there was no failing test:
// 2.67:1 is not detectably wrong without knowing what the user's theme does.
func TestEveryBackgroundColourIsThemeIndependent(t *testing.T) {
	source, err := os.ReadFile("tui.go")
	if err != nil {
		t.Fatalf("reading tui.go: %v", err)
	}

	matches := backgroundCall.FindAllSubmatch(source, -1)
	if len(matches) == 0 {
		t.Fatal("no .Background( calls found; this test has stopped testing anything")
	}

	for _, match := range matches {
		expr := string(match[1])
		field := strings.TrimPrefix(expr, "p.")

		value, ok := paletteColour(field)
		if !ok {
			t.Errorf("background %q is not a palette field; add a range rule for it", expr)
			continue
		}

		index, err := strconv.Atoi(value)
		if err != nil {
			t.Errorf("palette.%s is %q, which is not a 256-colour index", field, value)
			continue
		}
		if index < 16 || index > 255 {
			t.Errorf("palette.%s is %d, used as a background.\n"+
				"Indices 0-15 are the ANSI range and a terminal theme remaps them, so the\n"+
				"foreground chosen to sit on this cannot be guaranteed to contrast.\n"+
				"Use 16-255, which is fixed RGB.", field, index)
		}
	}
}

// paletteColour reads one field of defaultPalette as its raw index string.
func paletteColour(field string) (string, bool) {
	value := reflect.ValueOf(defaultPalette).FieldByName(field)
	if !value.IsValid() {
		return "", false
	}
	colour, ok := value.Interface().(lipgloss.TerminalColor)
	if !ok {
		return "", false
	}
	if colour == nil {
		return "", false
	}
	index, ok := colour.(lipgloss.Color)
	if !ok {
		return "", false
	}
	return string(index), true
}

// TestSelectionRowMeetsWCAGAA is the legibility contract, and it is checkable
// because SelectedBg is now in the fixed range.
//
// 4.5:1 is AA for normal text. The panel renders body text at that size, so
// this is the floor the selection row has to clear, not a nice-to-have.
func TestSelectionRowMeetsWCAGAA(t *testing.T) {
	const aaNormalText = 4.5

	fg, okFg := paletteColour("SelectedFg")
	bg, okBg := paletteColour("SelectedBg")
	if !okFg || !okBg {
		t.Fatalf("SelectedFg or SelectedBg is not a palette colour (fg ok=%v, bg ok=%v)", okFg, okBg)
	}

	ratio, err := contrastRatio(fg, bg)
	if err != nil {
		t.Fatalf("SelectedFg on SelectedBg: %v", err)
	}
	if ratio < aaNormalText {
		t.Errorf("the selection row is %.2f:1, want at least %.1f:1 (WCAG AA for normal text)",
			ratio, aaNormalText)
	}
}

// TestContrastAssertionWouldHaveCaughtTheOldPalette is the test that stops the
// test above from becoming decorative.
//
// The point of the range rule is that the previous pair was illegible on a
// light-theme blue. If that no longer measures below the floor, then either the
// floor is too low to matter or this file is not measuring what it claims to.
func TestContrastAssertionWouldHaveCaughtTheOldPalette(t *testing.T) {
	// What was shipped: SelectedFg "230" on SelectedBg "4".
	//
	// "4" cannot be measured directly, which is the entire argument for the
	// range rule, so the theme's light blue stands in for it. Every reasonable
	// light blue fails.
	for _, themeBlue := range []string{
		"6b9ee8", // a light blue a modern theme maps ANSI blue to
		"87ceeb", // sky blue
		"add8e6", // light blue
	} {
		ratio, err := hexContrastRatio("ffffd7", themeBlue)
		if err != nil {
			t.Fatal(err)
		}
		if ratio >= 4.5 {
			t.Errorf("the old pair on #%s measured %.2f:1, which is above the floor; "+
				"either this theme blue is not representative or the floor in "+
				"TestSelectionRowMeetsWCAGAA is too low to catch anything", themeBlue, ratio)
		}
	}
}

// xtermRGB resolves a 256-colour index to its RGB.
//
// Only 16-255 resolve. Returning an error for 0-15 is deliberate and is the
// reason the range rule exists rather than a contrast assertion on every colour.
func xtermRGB(index int) (r, g, b int, err error) {
	if index < 0 || index > 255 {
		return 0, 0, 0, strconv.ErrRange
	}
	if index < 16 {
		return 0, 0, 0, errThemeRemapped
	}
	if index >= 232 {
		level := 8 + 10*(index-232)
		return level, level, level, nil
	}
	levels := [6]int{0, 95, 135, 175, 215, 255}
	offset := index - 16
	return levels[offset/36], levels[(offset/6)%6], levels[offset%6], nil
}

var errThemeRemapped = &themeRemappedError{}

type themeRemappedError struct{}

func (*themeRemappedError) Error() string {
	return "colour index is in the ANSI range 0-15 and a terminal theme remaps it, so its RGB is not knowable"
}

// relativeLuminance is the WCAG 2.x definition.
func relativeLuminance(r, g, b int) float64 {
	linear := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

// contrastRatio is the WCAG ratio between two 256-colour indices.
func contrastRatio(fgIndex, bgIndex string) (float64, error) {
	fg, err := indexRGB(fgIndex)
	if err != nil {
		return 0, err
	}
	bg, err := indexRGB(bgIndex)
	if err != nil {
		return 0, err
	}
	return ratioOf(fg, bg), nil
}

// indexRGB resolves one palette index string to an RGB triple.
func indexRGB(index string) (rgb, error) {
	parsed, err := strconv.Atoi(index)
	if err != nil {
		return rgb{}, err
	}
	r, g, b, err := xtermRGB(parsed)
	if err != nil {
		return rgb{}, err
	}
	return rgb{r, g, b}, nil
}

// hexContrastRatio is contrastRatio for two #rrggbb literals, used to measure a
// theme colour that has no index.
func hexContrastRatio(fgHex, bgHex string) (float64, error) {
	fg, err := hexRGB(fgHex)
	if err != nil {
		return 0, err
	}
	bg, err := hexRGB(bgHex)
	if err != nil {
		return 0, err
	}
	return ratioOf(fg, bg), nil
}

type rgb struct{ r, g, b int }

func ratioOf(a, b rgb) float64 {
	la := relativeLuminance(a.r, a.g, a.b)
	lb := relativeLuminance(b.r, b.g, b.b)
	hi, lo := max(la, lb), min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

func hexRGB(hex string) (rgb, error) {
	value, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return rgb{}, err
	}
	return rgb{int(value >> 16), int((value >> 8) & 0xff), int(value & 0xff)}, nil
}
