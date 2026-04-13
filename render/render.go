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

const defaultSimilarityThreshold = 0.5

// config holds resolved options for a render operation.
type config struct {
	similarityThreshold float64
}

// defaultConfig returns a config with package defaults.
func defaultConfig() config {
	return config{similarityThreshold: defaultSimilarityThreshold}
}

// Option is a functional option that configures a render operation.
type Option func(*config)

// WithSimilarityThreshold sets the minimum similarity ratio for intraline highlighting.
// ratio = 2*equalRunes / (len(removedRunes) + len(addedRunes)).
// Default is 0.5. Use 0.0 to always highlight; 1.0 to never highlight.
// Values outside [0.0, 1.0] are accepted: negative values force inline highlighting
// on all pairs; values above 1.0 suppress it entirely.
func WithSimilarityThreshold(t float64) Option {
	return func(c *config) { c.similarityThreshold = t }
}

// applyOptions builds a config from defaults and the provided options.
func applyOptions(opts []Option) config {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	return cfg
}

// Render formats a diff.Diff as a colourised byte slice suitable for terminal output.
// Returns nil if d is the zero value (equal inputs). Options control intraline
// highlighting behaviour (e.g. [WithSimilarityThreshold]).
//
// Colour scheme:
//   - "diff …" and "@@ … @@" header lines are bold with no colour.
//   - "--- …" header lines are red; "+++ …" header lines are green.
//   - Context lines are unstyled with a double-space prefix.
//   - When a run of removed lines is immediately followed by an equal-length run
//     of added lines, each pair is diffed at the character level and changed
//     characters are highlighted with a coloured background.
//   - Otherwise whole-line colour is applied with no character-level highlighting.
func Render(d diff.Diff, opts ...Option) []byte {
	lines := d.Lines()
	if len(lines) == 0 {
		return nil
	}

	cfg := applyOptions(opts)

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
			buf, i = appendRemovedBlock(buf, lines, i, cfg)

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
func appendRemovedBlock(buf []byte, lines []diff.Line, i int, cfg config) ([]byte, int) {
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
		return renderInlinePairs(buf, removed, added, cfg), i
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
func renderInlinePairs(buf []byte, removed, added []diff.Line, cfg config) []byte {
	for k := range removed {
		ic := charDiff(removed[k].Content, added[k].Content, cfg)

		buf = styleRemovedLine.AppendText(buf, prefixRemoved)

		for _, seg := range ic.removed {
			if seg.changed {
				buf = styleRemovedHighlight.AppendText(buf, seg.text)
			} else {
				buf = styleRemovedLine.AppendText(buf, seg.text)
			}
		}

		buf = styleAddedLine.AppendText(buf, prefixAdded)

		for _, seg := range ic.added {
			if seg.changed {
				buf = styleAddedHighlight.AppendText(buf, seg.text)
			} else {
				buf = styleAddedLine.AppendText(buf, seg.text)
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
