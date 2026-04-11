package diff

import (
	"bytes"
	"unicode/utf8"
)

// Segment is a contiguous run of text from a line, tagged as equal or changed
// relative to the other side of the diff.
type Segment struct {
	Text    []byte // the raw bytes of this segment
	Changed bool   // true if this segment differs from the corresponding side
}

// InlineChange holds the character-level diff breakdown for a removed/added line pair.
//
// Removed and Added are parallel segment lists whose concatenated Text fields
// reconstruct the original removed and added lines respectively.
type InlineChange struct {
	Removed []Segment
	Added   []Segment
}

// CharDiff computes character-level diff segments for a removed/added line pair.
// It uses an O(mn) LCS algorithm on runes — acceptable since individual diff lines are short.
//
// Behaviour:
//   - Identical inputs return all-unchanged segments (Changed: false throughout).
//   - Trailing newlines are stripped before diffing and reattached afterwards.
//     A trailing newline is never placed inside a Changed segment to prevent ANSI
//     background colours bleeding onto the next terminal line when rendered.
//   - If either side contains invalid UTF-8, a single whole-line Changed segment
//     is returned as a fallback.
//   - If either side exceeds 500 runes, a single whole-line Changed segment is
//     returned to avoid O(mn) cost on minified or generated content.
//
// The concatenation of segment Text fields on each side always equals the
// original input byte-for-byte.
func CharDiff(removed, added []byte) InlineChange {
	// Identical inputs: short-circuit before any UTF-8 handling. This also
	// covers identical invalid UTF-8, where fallback would wrongly return
	// Changed:true segments.
	if bytes.Equal(removed, added) {
		seg := Segment{Text: bytes.Clone(removed), Changed: false}

		return InlineChange{
			Removed: []Segment{seg},
			Added:   []Segment{{Text: bytes.Clone(added), Changed: false}},
		}
	}

	// Strip trailing newline, remember whether each side had one.
	removedNL := len(removed) > 0 && removed[len(removed)-1] == '\n'
	addedNL := len(added) > 0 && added[len(added)-1] == '\n'

	removedCore := removed
	if removedNL {
		removedCore = removed[:len(removed)-1]
	}

	addedCore := added
	if addedNL {
		addedCore = added[:len(added)-1]
	}

	// Fall back to whole-line diff for invalid UTF-8: converting invalid bytes
	// to runes replaces them with U+FFFD, making it impossible to reconstruct
	// the original bytes from the segments.
	if !utf8.Valid(removedCore) || !utf8.Valid(addedCore) {
		return fallback(removed, added)
	}

	oldRunes := []rune(string(removedCore))
	newRunes := []rune(string(addedCore))

	// Safety cap: avoid O(mn) on large inputs (minified/generated content).
	if len(oldRunes) > 500 || len(newRunes) > 500 {
		return fallback(removed, added)
	}

	segs := lcsSegments(oldRunes, newRunes)

	removedSegs := reattachNL(segs.removed, removedNL)
	addedSegs := reattachNL(segs.added, addedNL)

	return InlineChange{Removed: removedSegs, Added: addedSegs}
}

// fallback returns a single Changed segment per side (whole-line fallback).
// Copies the input slices to avoid aliasing the caller's memory.
func fallback(removed, added []byte) InlineChange {
	result := InlineChange{}

	if len(removed) > 0 {
		cp := make([]byte, len(removed))
		copy(cp, removed)
		result.Removed = []Segment{{Text: cp, Changed: true}}
	}

	if len(added) > 0 {
		cp := make([]byte, len(added))
		copy(cp, added)
		result.Added = []Segment{{Text: cp, Changed: true}}
	}

	return result
}

// reattachNL returns a new segment slice with a newline reattached after the
// last segment. The input slice is not modified.
//
// If the last segment is Changed, the newline is appended as a separate
// unchanged segment rather than being included in the highlight span — a
// highlighted \n causes the ANSI background colour to bleed onto the next
// terminal line.
func reattachNL(segs []Segment, hadNL bool) []Segment {
	if !hadNL {
		return segs
	}

	if len(segs) == 0 {
		return []Segment{{Text: []byte{'\n'}, Changed: false}}
	}

	last := segs[len(segs)-1]

	if last.Changed {
		result := make([]Segment, 0, len(segs)+1)
		result = append(result, segs...)

		return append(result, Segment{Text: []byte{'\n'}, Changed: false})
	}

	result := make([]Segment, len(segs))
	copy(result, segs)

	last.Text = append(bytes.Clone(last.Text), '\n')
	result[len(result)-1] = last

	return result
}

// sides holds parallel removed/added segment lists built during backtracking.
type sides struct {
	removed []Segment
	added   []Segment
}

// op is a single edit operation produced during LCS backtracking.
type op struct {
	r    rune
	kind byte // 'e' equal, 'd' delete, 'i' insert
}

// lcsSegments builds the LCS table and backtracks to produce Equal/Delete/Insert
// edit operations, then merges consecutive same-kind ops into Segment runs.
func lcsSegments(old, newText []rune) sides {
	m, n := len(old), len(newText)

	// Build LCS DP table using a flat slice to avoid m+1 separate allocations.
	dp := make([]int, (m+1)*(n+1))

	stride := n + 1
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == newText[j-1] {
				dp[i*stride+j] = dp[(i-1)*stride+(j-1)] + 1
			} else {
				dp[i*stride+j] = max(dp[(i-1)*stride+j], dp[i*stride+(j-1)])
			}
		}
	}

	// Backtrack to build edit ops, then reverse.
	ops := make([]op, 0, m+n)

	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && old[i-1] == newText[j-1]:
			ops = append(ops, op{r: old[i-1], kind: 'e'})
			i--
			j--
		case j > 0 && (i == 0 || dp[i*stride+(j-1)] >= dp[(i-1)*stride+j]):
			ops = append(ops, op{r: newText[j-1], kind: 'i'})
			j--
		default:
			ops = append(ops, op{r: old[i-1], kind: 'd'})
			i--
		}
	}

	// Reverse ops (they were built backwards).
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	return mergeSegments(ops)
}

// segmentInitCap is the initial capacity for segment slices in mergeSegments.
// Most diff lines produce far fewer than 8 segments; this avoids the first
// few re-allocations on typical input.
const segmentInitCap = 8

// mergeSegments merges consecutive same-kind ops into Segment runs.
func mergeSegments(ops []op) sides {
	removedSegs := make([]Segment, 0, segmentInitCap)
	addedSegs := make([]Segment, 0, segmentInitCap)

	var buf [utf8.UTFMax]byte
	for _, o := range ops {
		nb := utf8.EncodeRune(buf[:], o.r)
		r := buf[:nb]

		switch o.kind {
		case 'e':
			if len(removedSegs) > 0 && !removedSegs[len(removedSegs)-1].Changed {
				removedSegs[len(removedSegs)-1].Text = append(removedSegs[len(removedSegs)-1].Text, r...)
			} else {
				removedSegs = append(removedSegs, Segment{Text: bytes.Clone(r), Changed: false})
			}

			if len(addedSegs) > 0 && !addedSegs[len(addedSegs)-1].Changed {
				addedSegs[len(addedSegs)-1].Text = append(addedSegs[len(addedSegs)-1].Text, r...)
			} else {
				addedSegs = append(addedSegs, Segment{Text: bytes.Clone(r), Changed: false})
			}
		case 'd':
			if len(removedSegs) > 0 && removedSegs[len(removedSegs)-1].Changed {
				removedSegs[len(removedSegs)-1].Text = append(removedSegs[len(removedSegs)-1].Text, r...)
			} else {
				removedSegs = append(removedSegs, Segment{Text: bytes.Clone(r), Changed: true})
			}
		case 'i':
			if len(addedSegs) > 0 && addedSegs[len(addedSegs)-1].Changed {
				addedSegs[len(addedSegs)-1].Text = append(addedSegs[len(addedSegs)-1].Text, r...)
			} else {
				addedSegs = append(addedSegs, Segment{Text: bytes.Clone(r), Changed: true})
			}
		default:
			// op.kind is always 'e', 'd', or 'i' — lcsSegments is the only producer.
		}
	}

	// Handle empty inputs: produce a single unchanged empty segment so callers
	// always have at least one segment to work with.
	if len(removedSegs) == 0 {
		removedSegs = append(removedSegs, Segment{Text: []byte{}, Changed: false})
	}

	if len(addedSegs) == 0 {
		addedSegs = append(addedSegs, Segment{Text: []byte{}, Changed: false})
	}

	return sides{removed: removedSegs, added: addedSegs}
}
