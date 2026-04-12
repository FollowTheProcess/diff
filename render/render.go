// Package render provides colourised terminal rendering of diff output.
//
// It depends on go.followtheprocess.codes/hue for ANSI colour formatting and is
// provided as a separate subpackage so consumers who only need raw diff output do
// not pull in the colour dependency.
package render

import (
	"go.followtheprocess.codes/diff"
	"go.followtheprocess.codes/hue"
)

const (
	styleHeaderBold       = hue.Bold
	styleRemovedHeader    = hue.Red
	styleAddedHeader      = hue.Green
	styleRemovedLine      = hue.Red
	styleAddedLine        = hue.Green
	styleRemovedHighlight = hue.Black | hue.Bold | hue.RedBackground
	styleAddedHighlight   = hue.Black | hue.Bold | hue.GreenBackground
)

var (
	prefixRemoved = []byte("- ")
	prefixAdded   = []byte("+ ")
)

// Render formats a []diff.Line as a colourised byte slice suitable for terminal output.
// Returns nil if lines is nil or empty.
//
// Colour scheme:
//   - "diff …" and "@@ … @@" header lines are bold with no colour.
//   - "--- …" header lines are red; "+++ …" header lines are green.
//   - Context lines are unstyled with a double-space prefix.
//   - When a run of removed lines is immediately followed by an equal-length run
//     of added lines, each pair is diffed at the character level via [diff.CharDiff]
//     and changed characters are highlighted with a coloured background.
//   - Otherwise whole-line colour is applied with no character-level highlighting.
func Render(lines []diff.Line) []byte {
	if len(lines) == 0 {
		return nil
	}

	var buf []byte

	i := 0
	for i < len(lines) {
		line := lines[i]

		switch line.Kind {
		case diff.KindHeader:
			buf = appendHeader(buf, line)
			i++

		case diff.KindContext:
			buf = appendContext(buf, line)
			i++

		case diff.KindRemoved:
			buf, i = appendRemovedBlock(buf, lines, i)

		case diff.KindAdded:
			buf, i = appendAddedBlock(buf, lines, i)

		default:
			// no action for unknown line kinds
			i++
		}
	}

	return buf
}

// appendHeader appends a styled header line to buf and returns the result.
func appendHeader(buf []byte, line diff.Line) []byte {
	if len(line.Content) >= 3 && line.Content[0] == '-' && line.Content[1] == '-' && line.Content[2] == '-' {
		return styleRemovedHeader.AppendText(buf, line.Content)
	}

	if len(line.Content) >= 3 && line.Content[0] == '+' && line.Content[1] == '+' && line.Content[2] == '+' {
		return styleAddedHeader.AppendText(buf, line.Content)
	}

	return styleHeaderBold.AppendText(buf, line.Content)
}

// appendContext appends an unstyled context line with a double-space prefix to buf.
func appendContext(buf []byte, line diff.Line) []byte {
	buf = append(buf, ' ', ' ')

	return append(buf, line.Content...)
}

// appendRemovedBlock collects a consecutive run of removed lines and any immediately
// following added lines starting at index i, renders them, and returns the updated buf and index.
func appendRemovedBlock(buf []byte, lines []diff.Line, i int) ([]byte, int) {
	start := i
	for i < len(lines) && lines[i].Kind == diff.KindRemoved {
		i++
	}

	removedEnd := i
	for i < len(lines) && lines[i].Kind == diff.KindAdded {
		i++
	}

	removed := lines[start:removedEnd]
	added := lines[removedEnd:i]

	if len(removed) == len(added) {
		return renderInlinePairs(buf, removed, added), i
	}

	return renderWholeLine(buf, removed, added), i
}

// appendAddedBlock collects a consecutive run of standalone added lines starting at index i,
// renders them with whole-line colour, and returns the updated buf and index.
func appendAddedBlock(buf []byte, lines []diff.Line, i int) ([]byte, int) {
	start := i
	for i < len(lines) && lines[i].Kind == diff.KindAdded {
		i++
	}

	return renderWholeLine(buf, nil, lines[start:i]), i
}

// renderInlinePairs renders 1:1 paired removed/added lines with character-level inline diff.
func renderInlinePairs(buf []byte, removed, added []diff.Line) []byte {
	for k := range removed {
		ic := diff.CharDiff(removed[k].Content, added[k].Content)

		buf = styleRemovedLine.AppendText(buf, prefixRemoved)

		for _, seg := range ic.Removed {
			if seg.Changed {
				buf = styleRemovedHighlight.AppendText(buf, seg.Text)
			} else {
				buf = styleRemovedLine.AppendText(buf, seg.Text)
			}
		}

		buf = styleAddedLine.AppendText(buf, prefixAdded)

		for _, seg := range ic.Added {
			if seg.Changed {
				buf = styleAddedHighlight.AppendText(buf, seg.Text)
			} else {
				buf = styleAddedLine.AppendText(buf, seg.Text)
			}
		}
	}

	return buf
}

// renderWholeLine renders removed/added lines with whole-line colour (no inline diff).
func renderWholeLine(buf []byte, removed, added []diff.Line) []byte {
	for _, r := range removed {
		buf = styleRemovedLine.AppendText(buf, prefixRemoved)
		buf = styleRemovedLine.AppendText(buf, r.Content)
	}

	for _, a := range added {
		buf = styleAddedLine.AppendText(buf, prefixAdded)
		buf = styleAddedLine.AppendText(buf, a.Content)
	}

	return buf
}
