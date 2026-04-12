// Package diff provides an anchored diff algorithm with structured line output
// and a plain unified-diff formatter.
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
package diff // import "go.followtheprocess.codes/diff"

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
// passed to [New]; callers must not modify it.
type Line struct {
	Content []byte
	Kind    LineKind
}

const (
	// defaultContextLines is the default number of unchanged context lines shown around each change.
	defaultContextLines = 3
)

// config holds resolved options for a diff operation.
type config struct {
	contextLines int
}

// defaultConfig returns a config with package defaults.
func defaultConfig() config {
	return config{contextLines: defaultContextLines}
}

// Option is a functional option that configures a diff operation.
type Option func(*config)

// WithContext sets the number of unchanged context lines shown around each change.
// The default is 3. Pass 0 to suppress context entirely.
func WithContext(n int) Option {
	return func(c *config) { c.contextLines = n }
}

// applyOptions builds a config from defaults and the provided options.
func applyOptions(opts []Option) config {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	return cfg
}

// Diff is the result of comparing two texts. The zero value represents no changes
// (identical inputs). Use [New] to compute a diff.
type Diff struct {
	lines []Line // nil when inputs are equal
}

// New computes an anchored diff between old and new and returns a Diff.
// Call [Diff.Equal] to test whether the inputs were identical.
func New(oldName string, old []byte, newName string, newText []byte, opts ...Option) Diff {
	if bytes.Equal(old, newText) {
		return Diff{}
	}

	return Diff{lines: computeLines(oldName, old, newName, newText, applyOptions(opts))}
}

// Equal reports whether the two inputs were identical (no changes).
func (d Diff) Equal() bool {
	return len(d.lines) == 0
}

// Lines returns the structured diff lines, or nil if the inputs were equal.
//
// The returned slice contains [KindHeader] lines for the diff/---/+++/@@ metadata,
// followed by [KindContext], [KindRemoved], and [KindAdded] lines for the diff body.
// Content on each [Line] holds the raw line text without any diff prefix.
func (d Diff) Lines() []Line {
	return d.lines
}

// String implements [fmt.Stringer], returning the diff as a plain unified-diff string.
// Returns an empty string when the inputs were equal.
func (d Diff) String() string {
	if d.Equal() {
		return ""
	}

	var out bytes.Buffer

	for _, line := range d.lines {
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

	return out.String()
}

// pair is a pair of line indexes, one for each side of the diff.
type pair struct{ x, y int }

// groupIntoHunks groups the unique-line match pairs returned by [tgs] into
// context-annotated diff Lines.
// pairs is the output of [tgs] (including sentinels). x and y are the full line
// slices. contextLines controls how many unchanged lines are shown around changes.
func groupIntoHunks(pairs []pair, x, y [][]byte, contextLines int) []Line {
	var (
		done   pair
		chunk  pair
		count  pair
		ctext  []Line
		result []Line
	)

	for _, m := range pairs {
		// Guard both coordinates independently: after pair offsetting a pair can
		// advance one axis past done while the other lags, producing a negative
		// slice range in the done.x:start.x or done.y:start.y expressions below.
		if m.x < done.x || m.y < done.y {
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

// trimResult is the output of trimPrefixSuffix.
type trimResult struct {
	oldTrimmed [][]byte
	newTrimmed [][]byte
	prefix     int
	suffix     int
}

// isDisjoint reports whether old and new share no lines in common.
// It only performs the check when both slices have at least 512 lines;
// below that threshold [tgs] is fast enough that the overhead is not worthwhile.
func isDisjoint(old, newSlice [][]byte) bool {
	if len(old) < 512 || len(newSlice) < 512 {
		return false
	}

	// Build a set from the shorter side to minimise allocations.
	shorter, longer := old, newSlice
	if len(old) > len(newSlice) {
		shorter, longer = newSlice, old
	}

	seen := make(map[string]struct{}, len(shorter))
	for _, s := range shorter {
		seen[unsafe.String(unsafe.SliceData(s), len(s))] = struct{}{}
	}

	for _, s := range longer {
		if _, ok := seen[unsafe.String(unsafe.SliceData(s), len(s))]; ok {
			return false
		}
	}

	return true
}

// trimPrefixSuffix finds the longest common prefix and suffix of x and y and
// returns the middle "interesting" portion together with the strip counts.
// The trimmed slices are subslices of the originals — no allocation.
func trimPrefixSuffix(x, y [][]byte) trimResult {
	prefix := 0
	for prefix < len(x) && prefix < len(y) && bytes.Equal(x[prefix], y[prefix]) {
		prefix++
	}

	suffix := 0
	for suffix < len(x)-prefix && suffix < len(y)-prefix &&
		bytes.Equal(x[len(x)-1-suffix], y[len(y)-1-suffix]) {
		suffix++
	}

	return trimResult{
		prefix:     prefix,
		oldTrimmed: x[prefix : len(x)-suffix],
		newTrimmed: y[prefix : len(y)-suffix],
		suffix:     suffix,
	}
}

// compact post-processes grouped diff lines, shifting change blocks to semantic
// boundaries. For each contiguous block of KindRemoved (or KindAdded) lines, if
// the last line in the block has the same content as the KindContext line
// immediately following it, the block shifts down by one: the first line of the
// block is re-classified as KindContext and the formerly-context line is
// re-classified as the changed kind. This repeats until stable.
//
// compact never moves changes across hunk boundaries (KindHeader lines act as
// natural stops since the inner loop only shifts when the next line is KindContext).
func compact(lines []Line) []Line {
	result := make([]Line, len(lines))
	copy(result, lines)

	for {
		shifted := false

		i := 0
		for i < len(result) {
			kind := result[i].Kind
			if kind != KindRemoved && kind != KindAdded {
				i++
				continue
			}
			// Find end of contiguous block of this kind.
			j := i + 1
			for j < len(result) && result[j].Kind == kind {
				j++
			}
			// j is the index of the first line after the block.
			// Shift down when last line in block == first context line after it.
			if j < len(result) && result[j].Kind == KindContext &&
				bytes.Equal(result[j-1].Content, result[j].Content) {
				result[i].Kind = KindContext
				result[j].Kind = kind
				shifted = true
				i++ // advance past the newly-Context line; the freshly-reclassified j will be found on next scan

				continue
			}

			i = j
		}

		if !shifted {
			break
		}
	}

	return result
}

// computeLines computes structured diff lines for old and newText (assumed non-equal).
func computeLines(oldName string, old []byte, newName string, newText []byte, cfg config) []Line {
	x := splitLines(old)
	y := splitLines(newText)

	// diffHeaders is the number of fixed header lines prepended to every diff result:
	// "diff …", "--- …", and "+++ …".
	const diffHeaders = 3

	headers := []Line{
		{Kind: KindHeader, Content: fmt.Appendf(nil, "diff %s %s\n", oldName, newName)},
		{Kind: KindHeader, Content: fmt.Appendf(nil, "--- %s\n", oldName)},
		{Kind: KindHeader, Content: fmt.Appendf(nil, "+++ %s\n", newName)},
	}

	tr := trimPrefixSuffix(x, y)

	if isDisjoint(tr.oldTrimmed, tr.newTrimmed) {
		// Fast-path: no common lines — emit one hunk with all removals then all additions.
		body := make([]Line, 0, 1+len(tr.oldTrimmed)+len(tr.newTrimmed))

		body = append(body, chunkHeader(
			pair{tr.prefix, tr.prefix},
			pair{len(tr.oldTrimmed), len(tr.newTrimmed)},
		))
		for _, s := range tr.oldTrimmed {
			body = append(body, Line{Kind: KindRemoved, Content: s})
		}

		for _, s := range tr.newTrimmed {
			body = append(body, Line{Kind: KindAdded, Content: s})
		}

		body = compact(body)
		result := make([]Line, 0, diffHeaders+len(body))
		result = append(result, headers...)
		result = append(result, body...)

		return result
	}

	// Run TGS on the trimmed middle only, then offset the returned pairs back
	// into full-array coordinates so groupIntoHunks draws context from the
	// correct positions (including up to contextLines lines from the prefix).
	pairs := tgs(tr.oldTrimmed, tr.newTrimmed)
	for i := range pairs {
		pairs[i].x += tr.prefix
		pairs[i].y += tr.prefix
	}

	hunks := groupIntoHunks(pairs, x, y, cfg.contextLines)
	hunks = compact(hunks)
	result := make([]Line, 0, diffHeaders+len(hunks))
	result = append(result, headers...)
	result = append(result, hunks...)

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
