package dashboard

import (
	"os"
	"strings"
	"testing"
)

const statusDoc = "../../../docs/guides/statuses.md"

// renderStateDoc is the generated part of docs/guides/statuses.md: one table
// per group of StateGuide, so the guide cannot drift from the badges.
func renderStateDoc() string {
	var b strings.Builder
	for _, g := range StateGuide {
		b.WriteString("\n### " + g.Title + "\n\nShown on " + g.Where + ".\n\n")
		b.WriteString("| Badge | Meaning | What to do |\n|---|---|---|\n")
		for _, s := range g.States {
			next := s.Next
			if next == "" {
				next = "Nothing."
			}
			b.WriteString("| `" + s.Label + "` | " + s.Meaning + " | " + next + " |\n")
		}
	}
	return b.String() + "\n"
}

func TestStatusGuideMatchesTheDocs(t *testing.T) {
	raw, err := os.ReadFile(statusDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, "<!-- states:start")
	end := strings.Index(doc, "<!-- states:end -->")
	if start < 0 || end < start {
		t.Fatalf("%s lost its states:start / states:end markers", statusDoc)
	}
	head := start + strings.Index(doc[start:], "-->") + len("-->\n")
	want := renderStateDoc()
	if doc[head:end] == want {
		return
	}
	if os.Getenv("MDB_UPDATE_DOCS") == "1" {
		if err := os.WriteFile(statusDoc, []byte(doc[:head]+want+doc[end:]), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Errorf("%s is out of date with StateGuide; regenerate it: MDB_UPDATE_DOCS=1 go -C src test ./internal/dashboard/ -run TestStatusGuideMatchesTheDocs", statusDoc)
}

func TestEveryStateHasAMeaningAndAKnownTone(t *testing.T) {
	tones := map[string]bool{"green": true, "yellow": true, "azure": true, "red": true, "secondary": true}
	n := 0
	for _, g := range StateGuide {
		for _, s := range g.States {
			n++
			if s.Label == "" || s.Meaning == "" || !tones[s.Tone] {
				t.Errorf("%s/%s: label %q, tone %q, meaning %q", g.ID, s.Key, s.Label, s.Tone, s.Meaning)
			}
		}
	}
	if n < 15 {
		t.Fatalf("the guide lists %d states; expected every engine, health, project, job and Live state", n)
	}
	if got := stateOf("engine", "orphan").Label; got != "leftover" {
		t.Errorf("an orphaned engine reads %q; the badge says leftover", got)
	}
}
