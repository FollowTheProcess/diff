// Package diff provides an anchored diff algorithm with structured line output,
// character-level inline diff, and a plain unified-diff formatter.
//
// The core algorithm is derived from the Go standard library's internal/diff package
// (https://github.com/golang/go/tree/master/src/internal/diff).
// Copyright 2022 The Go Authors. All rights reserved. Used under a BSD-style licence.
//
// An anchored diff finds the diff with the smallest number of "unique" lines
// inserted and removed, where unique means a line appears exactly once in both old
// and new. Unique lines anchor the matching regions, producing cleaner output than
// standard diff — blank lines and closing braces are not reused as false anchors.
// The algorithm runs in O(n log n) rather than the standard O(n²).
package diff

import (
	"bytes"
	"fmt"
	"sort"
	"unsafe"
)

// LineKind identifies the role of a line in a diff output.
type LineKind int

// LineKind values that categorise each line of a diff.
const (
	KindContext LineKind = iota // an unchanged context line shown for surrounding context
	KindRemoved                 // a line present only in the old (left) side
	KindAdded                   // a line present only in the new (right) side
	KindHeader                  // a diff metadata line: "diff …", "--- …", "+++ …", or "@@ … @@"
)

// String implements fmt.Stringer, returning the name of the constant (e.g. "KindAdded").
func (k LineKind) String() string {
	switch k {
	case KindContext:
		return "KindContext"
	case KindRemoved:
		return "KindRemoved"
	case KindAdded:
		return "KindAdded"
	case KindHeader:
		return "KindHeader"
	default:
		return fmt.Sprintf("LineKind(%d)", int(k))
	}
}

// Line is a single structured line from a diff.
//
// Content holds the line text without the leading diff prefix ("- "/"+ "/"  ").
// For KindHeader lines, Content holds the full raw line including its newline.
// For all other kinds, Content holds the line text as it appeared in the source,
// including its trailing newline if present. Content aliases the original input
// passed to [Lines] or [Diff]; callers must not modify it.
type Line struct {
	Content []byte
	Kind    LineKind
}

// pair is a pair of line indexes, one for each side of the diff.
type pair struct{ x, y int }

// Lines returns the structured diff lines for old and newText.
// Returns nil if old and newText are identical.
//
// The returned slice contains KindHeader lines for the diff/---/+++/@@ metadata,
// followed by KindContext, KindRemoved, and KindAdded lines for the diff body.
// Content on each Line holds the raw line text without any diff prefix.
//
// The structured form is useful for custom rendering; for plain unified-diff
// text output use [Diff].
func Lines(oldName string, old []byte, newName string, newText []byte) []Line {
	if bytes.Equal(old, newText) {
		return nil
	}

	return computeLines(oldName, old, newName, newText)
}

// Diff returns an anchored unified diff of old and newText as raw bytes.
// Returns nil if old and newText are identical.
//
// Unix diff implementations typically look for a diff with the smallest number
// of lines inserted and removed, which can in the worst case take time quadratic
// in the number of lines. As a result, many implementations either can be made to
// run for a long time or cut off the search after a predetermined amount of work.
//
// In contrast, this implementation looks for a diff with the smallest number of
// "unique" lines inserted and removed, where unique means a line that appears just
// once in both old and new. We call this an "anchored diff" because the unique
// lines anchor the chosen matching regions. An anchored diff is usually clearer
// than a standard diff, because the algorithm does not try to reuse unrelated blank
// lines or closing braces. The algorithm also guarantees to run in O(n log n) time
// instead of the standard O(n²) time.
//
// Some systems call this approach a "patience diff," named for the "patience
// sorting" algorithm, itself named for a solitaire card game. We avoid that name
// for two reasons. First, the name has been used for a few different variants of
// the algorithm, so it is imprecise. Second, the name is frequently interpreted as
// meaning that you have to wait longer (to be patient) for the diff, meaning that
// it is a slower algorithm, when in fact the algorithm is faster than the standard one.
//
// For structured line-by-line output use [Lines].
func Diff(
	oldName string,
	old []byte,
	newName string,
	newText []byte,
) []byte {
	if bytes.Equal(old, newText) {
		return nil
	}

	structured := computeLines(oldName, old, newName, newText)

	var out bytes.Buffer

	for _, line := range structured {
		switch line.Kind {
		case KindHeader:
			out.Write(line.Content)
		case KindRemoved:
			out.WriteString("- ")
			out.Write(line.Content)
		case KindAdded:
			out.WriteString("+ ")
			out.Write(line.Content)
		case KindContext:
			out.WriteString("  ")
			out.Write(line.Content)
		default:
			// no action for unknown line kinds
		}
	}

	return out.Bytes()
}

// computeLines computes structured diff lines for old and newText (assumed non-equal).
func computeLines(oldName string, old []byte, newName string, newText []byte) []Line {
	x := splitLines(old)
	y := splitLines(newText)

	var result []Line

	result = append(result,
		Line{Kind: KindHeader, Content: fmt.Appendf(nil, "diff %s %s\n", oldName, newName)},
		Line{Kind: KindHeader, Content: fmt.Appendf(nil, "--- %s\n", oldName)},
		Line{Kind: KindHeader, Content: fmt.Appendf(nil, "+++ %s\n", newName)},
	)

	var (
		done  pair
		chunk pair
		count pair
		ctext []Line
	)

	const contextLines = 3

	for _, m := range tgs(x, y) {
		if m.x < done.x {
			continue
		}

		start, end := expandMatch(m, done, x, y)

		for _, s := range x[done.x:start.x] {
			ctext = append(ctext, Line{Kind: KindRemoved, Content: s})
			count.x++
		}

		for _, s := range y[done.y:start.y] {
			ctext = append(ctext, Line{Kind: KindAdded, Content: s})
			count.y++
		}

		if (end.x < len(x) || end.y < len(y)) &&
			(end.x-start.x < contextLines || (len(ctext) > 0 && end.x-start.x < 2*contextLines)) {
			for _, s := range x[start.x:end.x] {
				ctext = append(ctext, Line{Kind: KindContext, Content: s})
				count.x++
				count.y++
			}

			done = end

			continue
		}

		if len(ctext) > 0 {
			n := min(end.x-start.x, contextLines)

			for _, s := range x[start.x : start.x+n] {
				ctext = append(ctext, Line{Kind: KindContext, Content: s})
				count.x++
				count.y++
			}

			done = pair{start.x + n, start.y + n}

			result = append(result, chunkHeader(chunk, count))
			result = append(result, ctext...)

			count.x = 0
			count.y = 0
			ctext = ctext[:0]
		}

		if end.x >= len(x) && end.y >= len(y) {
			break
		}

		chunk = pair{end.x - contextLines, end.y - contextLines}
		for _, s := range x[chunk.x:end.x] {
			ctext = append(ctext, Line{Kind: KindContext, Content: s})
			count.x++
			count.y++
		}

		done = end
	}

	return result
}

// expandMatch expands a match region backward to start and forward to end
// while adjacent lines in x and y also match.
func expandMatch(m, done pair, x, y [][]byte) (start, end pair) {
	start = m
	for start.x > done.x && start.y > done.y && bytes.Equal(x[start.x-1], y[start.y-1]) {
		start.x--
		start.y--
	}

	end = m
	for end.x < len(x) && end.y < len(y) && bytes.Equal(x[end.x], y[end.y]) {
		end.x++
		end.y++
	}

	return start, end
}

// chunkHeader formats the @@ header line for a diff chunk.
func chunkHeader(chunk, count pair) Line {
	x, y := chunk.x, chunk.y
	if count.x > 0 {
		x++
	}

	if count.y > 0 {
		y++
	}

	return Line{
		Kind:    KindHeader,
		Content: fmt.Appendf(nil, "@@ -%d,%d +%d,%d @@\n", x, count.x, y, count.y),
	}
}

// splitLines returns the lines in x as subslices of x, including newlines.
// If the file does not end in a newline, a new slice is allocated for the final
// line with the standard "no newline" warning appended.
func splitLines(x []byte) [][]byte {
	var lines [][]byte

	for len(x) > 0 {
		i := bytes.IndexByte(x, '\n')
		if i < 0 {
			// No trailing newline — must allocate to append the warning suffix.
			const suffix = "\n\\ No newline at end of file\n"

			line := make([]byte, len(x)+len(suffix))
			copy(line, x)
			copy(line[len(x):], suffix)

			lines = append(lines, line)

			break
		}

		lines = append(lines, x[:i+1])
		x = x[i+1:]
	}

	return lines
}

// tgsYStep is the per-occurrence decrement applied to y-side entries in the
// tgs occurrence map. It is 4× the x-side step (1) so that x and y counts
// occupy separate bit ranges, allowing a unique line to be identified by the
// combined value -1 + -tgsYStep.
const tgsYStep = 4

// tgsYMany is the magnitude at which y-side occurrence counting is clamped
// (i.e. the line appears "many" times). Equivalent to two tgsYStep decrements.
const tgsYMany = tgsYStep * 2

// tgsSentinels is the number of sentinel pairs added to the result of tgs:
// one at the start {0,0} and one at the end {len(x),len(y)}.
const tgsSentinels = 2

// tgs returns the pairs of indexes of the longest common subsequence
// of unique lines in x and y, with sentinel pairs {0,0} and {len(x),len(y)}.
//
// Algorithm: Thomas G. Szymanski, "A Special Case of the Maximal Common
// Subsequence Problem," Princeton TR #170 (January 1975).
func tgs(x, y [][]byte) []pair {
	// unsafe.String converts a []byte to string without allocating. This is safe
	// because the strings are only stored in m, which is local to this function.
	// The backing []byte data (subslices of the original inputs) outlives this call.
	m := make(map[string]int)

	for _, s := range x {
		k := unsafe.String(unsafe.SliceData(s), len(s))
		if c := m[k]; c > -2 {
			m[k] = c - 1
		}
	}

	for _, s := range y {
		k := unsafe.String(unsafe.SliceData(s), len(s))
		if c := m[k]; c > -tgsYMany {
			m[k] = c - tgsYStep
		}
	}

	var xi, yi, inv []int

	for i, s := range y {
		k := unsafe.String(unsafe.SliceData(s), len(s))
		if m[k] == -1+-tgsYStep {
			m[k] = len(yi)
			yi = append(yi, i)
		}
	}

	for i, s := range x {
		k := unsafe.String(unsafe.SliceData(s), len(s))
		if j, ok := m[k]; ok && j >= 0 {
			xi = append(xi, i)
			inv = append(inv, j)
		}
	}

	j := inv
	n := len(xi)
	tails := make([]int, n)
	lengths := make([]int, n)

	for i := range tails {
		tails[i] = n + 1
	}

	for i := range n {
		k := sort.Search(n, func(k int) bool {
			return tails[k] >= j[i]
		})
		tails[k] = j[i]
		lengths[i] = k + 1
	}

	k := 0
	for _, v := range lengths {
		if k < v {
			k = v
		}
	}

	seq := make([]pair, tgsSentinels+k)
	seq[1+k] = pair{len(x), len(y)}

	lastj := n
	for i := n - 1; i >= 0; i-- {
		if lengths[i] == k && j[i] < lastj {
			seq[k] = pair{xi[i], yi[j[i]]}
			k--
			lastj = j[i]
		}
	}

	seq[0] = pair{0, 0}

	return seq
}
