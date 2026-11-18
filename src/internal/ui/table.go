package ui

import "strings"

// Table is a titled grid. Cells may carry colour; widths ignore it.
type Table struct {
	Title   string
	Headers []string
	Rows    [][]string
}

// Table draws t with rounded box borders, one string per line. Columns past
// maxWidth are truncated from the widest column first.
func (s Style) Table(t Table, maxWidth int) []string {
	cols := len(t.Headers)
	if cols == 0 {
		return nil
	}
	w := make([]int, cols)
	for i, h := range t.Headers {
		w[i] = Width(h)
	}
	for _, r := range t.Rows {
		for i := 0; i < cols && i < len(r); i++ {
			if cw := Width(r[i]); cw > w[i] {
				w[i] = cw
			}
		}
	}
	shrink(w, maxWidth)

	b := s.Dim
	line := func(l, m, r string) string {
		parts := make([]string, cols)
		for i := range w {
			parts[i] = strings.Repeat("─", w[i]+2)
		}
		return b(l + strings.Join(parts, m) + r)
	}
	row := func(cells []string, head bool) string {
		parts := make([]string, cols)
		for i := range w {
			c := ""
			if i < len(cells) {
				c = Truncate(cells[i], w[i])
			}
			if head {
				c = s.Bold(c)
			}
			parts[i] = " " + Pad(c, w[i]) + " "
		}
		return b("│") + strings.Join(parts, b("│")) + b("│")
	}

	var out []string
	if t.Title != "" {
		out = append(out, s.Bold(t.Title))
	}
	out = append(out, line("╭", "┬", "╮"), row(t.Headers, true), line("├", "┼", "┤"))
	for _, r := range t.Rows {
		out = append(out, row(r, false))
	}
	return append(out, line("╰", "┴", "╯"))
}

// shrink narrows the widest columns until the table, borders included, fits
// in max cells. A column never goes below four.
func shrink(w []int, maxWidth int) {
	if maxWidth <= 0 {
		return
	}
	total := func() int {
		t := 1
		for _, c := range w {
			t += c + 3
		}
		return t
	}
	for total() > maxWidth {
		widest := 0
		for i := range w {
			if w[i] > w[widest] {
				widest = i
			}
		}
		if w[widest] <= 4 {
			return
		}
		w[widest]--
	}
}
