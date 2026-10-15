package ci

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// bindingPair matches one entry of the list the `?` overlay is generated from.
var bindingPair = regexp.MustCompile(`\{"([^"]+)", "([^"]+)"\}`)

// keyAliases maps a key token to the other spellings the README may use. The
// source writes the word and the table writes the glyph, so demanding one exact
// spelling would fail on typography rather than on a missing row.
var keyAliases = map[string][]string{
	"up":    {"↑"},
	"down":  {"↓"},
	"left":  {"←"},
	"right": {"→"},
}

// tuiSource reads tui.go, the only file that declares a binding.
func tuiSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../server/tui.go")
	if err != nil {
		t.Fatalf("read tui.go: %v", err)
	}
	return string(raw)
}

// sliceOf returns the body of a top-level slice var, so a pattern is not
// applied to the whole file. An unbounded pattern also matches unrelated struct
// literals -- severityKeywords is {[]string{"info", "notice"}, severityInfo} --
// and reports them as unbound keys, which is a test that cries wolf.
func sliceOf(src, decl string) (string, bool) {
	start := strings.Index(src, decl)
	if start < 0 {
		return "", false
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		return body[:end], true
	}
	return body, true
}

// bindingKeys returns every key the TUI binds.
func bindingKeys(t *testing.T) []string {
	t.Helper()
	block, ok := sliceOf(tuiSource(t), "var keybindings = []keybinding{")
	if !ok {
		t.Fatal("tui.go no longer declares var keybindings = []keybinding{")
	}

	var keys []string
	for _, m := range bindingPair.FindAllStringSubmatch(block, -1) {
		// "q, ctrl+c" and "1/2/3" each name several keys.
		for _, k := range strings.FieldsFunc(m[1], func(r rune) bool {
			return r == ',' || r == '/'
		}) {
			if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
				keys = append(keys, k)
			}
		}
	}
	if len(keys) == 0 {
		t.Fatal("found no bindings inside the keybindings var")
	}
	return keys
}

// windowLabels returns the log time window's cycle order, read from the source
// rather than restated here. A test that hardcodes "5m, 1h, 24h, all" passes
// even after the order changes in tui.go, which is the drift this whole file
// exists to catch.
func windowLabels(t *testing.T) []string {
	t.Helper()
	block, ok := sliceOf(tuiSource(t), "var logWindowOptions = []struct {")
	if !ok {
		t.Fatal("tui.go no longer declares var logWindowOptions")
	}
	re := regexp.MustCompile(`\{"([^"]+)",`)
	var labels []string
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		labels = append(labels, m[1])
	}
	if len(labels) == 0 {
		t.Fatal("no labels found in logWindowOptions")
	}
	return labels
}

// keyTable returns the README keybinding table as rows keyed by the first key
// token in the row, lowercased.
func keyTable(t *testing.T) map[string]string {
	t.Helper()
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	start := strings.Index(string(readme), "| Key | Action |")
	if start < 0 {
		t.Fatal("README.md has no keybinding table")
	}
	table := string(readme)[start:]
	if end := strings.Index(table[1:], "\n\n"); end >= 0 {
		table = table[:end]
	}

	rows := map[string]string{}
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") ||
			strings.Contains(line, "| Key |") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(cells[0]))
		key = strings.Trim(key, "`")
		if key == "" {
			continue
		}
		action := strings.ToLower(strings.TrimSpace(cells[1]))
		// A row may lead with several keys, "tab / shift+tab" or "↑ / ↓ or k / j".
		// Register the row under each so a lookup by any of them finds it.
		// Backticks wrap each key, not the whole cell, so trim per part.
		parts := strings.FieldsFunc(key, func(r rune) bool {
			return r == '/' || r == ','
		})
		if len(parts) == 0 {
			rows[strings.Trim(key, "`")] = action
			continue
		}
		for _, k := range parts {
			if k = strings.Trim(strings.TrimSpace(k), "`"); k != "" {
				rows[k] = action
			}
		}
	}
	if len(rows) == 0 {
		t.Fatal("the README keybinding table parsed to no rows")
	}
	return rows
}

// mentionsKey reports whether text names key as a standalone token.
//
// A bare substring test is wrong here. "k" is a bound key and it appears
// inside the word "keybindings", so Contains would pass a table with no k row
// at all. Single-letter keys are the normal case, not the edge case.
func mentionsKey(text, key string) bool {
	if keyAliases[key] != nil {
		// The source says "up" and the table writes "↑". There is no
		// word-boundary problem with a glyph, so match it directly.
		for _, alias := range keyAliases[key] {
			if strings.Contains(text, alias) {
				return true
			}
		}
	}
	quoted := regexp.QuoteMeta(key)
	return regexp.MustCompile(`(^|[^` + "`" + `a-z0-9+])` + quoted + `([^` + "`" + `a-z0-9+]|$)`).MatchString(text)
}

// tableText returns the README keybinding table with backticks removed, so a
// key is compared against the words a reader sees rather than the markup
// around them.
func tableText(t *testing.T) string {
	t.Helper()
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	start := strings.Index(string(readme), "| Key | Action |")
	if start < 0 {
		t.Fatal("README.md has no keybinding table")
	}
	table := string(readme)[start:]
	if end := strings.Index(table[1:], "\n\n"); end >= 0 {
		table = table[:end]
	}
	return strings.ToLower(strings.ReplaceAll(table, "`", ""))
}

// TestEveryBoundKeyHasATableRow is the presence check: no bound key may be
// absent from the table.
//
// The `?` overlay is generated from tui.go, so it cannot be wrong. The table is
// hand-written, so it drifts, and nothing reports it.
func TestEveryBoundKeyHasATableRow(t *testing.T) {
	table := tableText(t)

	for _, key := range bindingKeys(t) {
		if !mentionsKey(table, key) {
			t.Errorf("the TUI binds %q but the README keybinding table never names it.\n"+
				"The `?` overlay is generated from tui.go, so the overlay has it and the\n"+
				"README does not.", key)
		}
	}
}

// TestTableRowsSayWhatTheOverlaySays is the part that actually matters, and
// the part presence cannot catch.
//
// A key listed with the wrong description is worse than an absent one, because
// a reader trusts it. The table had three:
//
//	Enter  "Open the selected file". On the agents panel it filters the logs
//	       to that agent, which is the only way to scope by agent at all.
//	t      "Cycle the log time window". The order is 5m, 1h, 24h, all and the
//	       default is all, so someone expecting five minutes by default is wrong.
//	/      "Filter loaded records". Inside the prompt, tab switches scope and
//	       ctrl+r switches to a regular expression. Neither was in the table.
func TestTableRowsSayWhatTheOverlaySays(t *testing.T) {
	rows := keyTable(t)

	t.Run("enter filters by agent", func(t *testing.T) {
		row, ok := rows["enter"]
		if !ok {
			t.Skip("no Enter row; TestEveryBoundKeyHasATableRow reports that")
		}
		if !strings.Contains(row, "agent") {
			t.Errorf("the Enter row says %q.\n"+
				"On the agents panel Enter filters the logs to the selected agent. That is\n"+
				"the only way to scope records by agent, and the row omits it.", row)
		}
	})

	t.Run("t states the order and the default", func(t *testing.T) {
		row, ok := rows["t"]
		if !ok {
			t.Skip("no t row; TestEveryBoundKeyHasATableRow reports that")
		}
		for _, label := range windowLabels(t) {
			if !strings.Contains(row, label) {
				t.Errorf("the t row does not mention %q, so the cycle order is wrong or absent.\n"+
					"Read from logWindowOptions in tui.go, the order is %s.\n"+
					"Row as written: %q",
					label, strings.Join(windowLabels(t), ", "), row)
			}
		}
		if !strings.Contains(row, "default") {
			t.Errorf("the t row does not state the default: %q.\n"+
				"The window starts on the last entry, which is %q, not on five minutes.",
				row, windowLabels(t)[len(windowLabels(t))-1])
		}
	})

	t.Run("slash names the prompt keys", func(t *testing.T) {
		row, ok := rows["/"]
		if !ok {
			t.Skip("no / row; TestEveryBoundKeyHasATableRow reports that")
		}
		for _, want := range []string{"tab", "ctrl+r"} {
			if !strings.Contains(row, want) {
				t.Errorf("the / row does not mention %q: %q.\n"+
					"Both act only while the filter prompt is open, so a table of panel keys\n"+
					"cannot show them and has to say so here.", want, row)
			}
		}
	})
}
