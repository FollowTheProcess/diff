package render

import (
	"bytes"
	"unicode/utf8"
)

// segment is a contiguous run of text from a line, tagged as equal or changed
// relative to the other side of the diff.
type segment struct {
	text    []byte // the raw bytes of this segment
	changed bool   // true if this segment differs from the corresponding side
}

// inlineChange holds the character-level diff breakdown for a removed/added line pair.
//
// removed and added are parallel segment lists whose concatenated text fields
// reconstruct the original removed and added lines respectively.
type inlineChange struct {
	removed []segment
	added   []segment
}

// charDiff computes character-level diff segments for a removed/added line pair.
// It uses an O(mn) LCS algorithm on runes — acceptable since individual diff lines are short.
//
// Behaviour:
//   - Identical inputs return all-unchanged segments (changed: false throughout).
//   - Trailing newlines are stripped before diffing and reattached afterwards.
//     A trailing newline is never placed inside a changed segment to prevent ANSI
//     background colours bleeding onto the next terminal line when rendered.
//   - If either side contains invalid UTF-8, a single whole-line changed segment
//     is returned as a fallback.
//   - If either side exceeds 500 runes, a single whole-line changed segment is
//     returned to avoid O(mn) cost on minified or generated content.
//   - If the similarity ratio (2*equalRunes / totalRunes) is below the threshold
//     set by [WithSimilarityThreshold] (default 0.5), a whole-line changed segment
//     is returned — intraline highlighting of very different lines is noisy.
//
// The concatenation of segment text fields on each side always equals the
// original input byte-for-byte.
func charDiff(removed, added []byte, cfg config) inlineChange {
	// Identical inputs: short-circuit before any UTF-8 handling. This also
	// covers identical invalid UTF-8, where fallback would wrongly return
	// changed:true segments.
	if bytes.Equal(removed, added) {
		seg := segment{text: bytes.Clone(removed), changed: false}

		return inlineChange{
			removed: []segment{seg},
			added:   []segment{{text: bytes.Clone(added), changed: false}},
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

	segs := lcsSegments(oldRunes, newRunes, len(removedCore), len(addedCore))

	// Similarity ratio gate: if the ratio of equal runes to total runes is below
	// the threshold, intraline highlighting would be more noise than signal.
	// Build the whole-line fallback using the stripped core slices so that
	// reattachNL can place the trailing newline outside the changed segment —
	// a highlighted \n bleeds ANSI background colour onto the next terminal line.
	totalRunes := len(oldRunes) + len(newRunes)
	if totalRunes > 0 {
		equalRunes := 0

		for _, seg := range segs.removed {
			if !seg.changed {
				equalRunes += utf8.RuneCount(seg.text)
			}
		}

		ratio := float64(similarityRatioScale*equalRunes) / float64(totalRunes)
		if ratio < cfg.similarityThreshold {
			var rSegs []segment
			if len(removedCore) > 0 {
				rSegs = []segment{{text: bytes.Clone(removedCore), changed: true}}
			}

			var aSegs []segment
			if len(addedCore) > 0 {
				aSegs = []segment{{text: bytes.Clone(addedCore), changed: true}}
			}

			return inlineChange{
				removed: reattachNL(rSegs, removedNL),
				added:   reattachNL(aSegs, addedNL),
			}
		}
	}

	removedSegs := reattachNL(segs.removed, removedNL)
	addedSegs := reattachNL(segs.added, addedNL)

	return inlineChange{removed: removedSegs, added: addedSegs}
}

// fallback returns a single changed segment per side (whole-line fallback).
// Copies the input slices to avoid aliasing the caller's memory.
func fallback(removed, added []byte) inlineChange {
	result := inlineChange{}

	if len(removed) > 0 {
		cp := make([]byte, len(removed))
		copy(cp, removed)
		result.removed = []segment{{text: cp, changed: true}}
	}

	if len(added) > 0 {
		cp := make([]byte, len(added))
		copy(cp, added)
		result.added = []segment{{text: cp, changed: true}}
	}

	return result
}

// reattachNL returns a new segment slice with a newline reattached after the
// last segment. The input slice is not modified.
//
// If the last segment is changed, the newline is appended as a separate
// unchanged segment rather than being included in the highlight span — a
// highlighted \n causes the ANSI background colour to bleed onto the next
// terminal line.
func reattachNL(segs []segment, hadNL bool) []segment {
	if !hadNL {
		return segs
	}

	if len(segs) == 0 {
		return []segment{{text: []byte{'\n'}, changed: false}}
	}

	last := segs[len(segs)-1]

	if last.changed {
		result := make([]segment, 0, len(segs)+1)
		result = append(result, segs...)

		return append(result, segment{text: []byte{'\n'}, changed: false})
	}

	result := make([]segment, len(segs))
	copy(result, segs)

	last.text = append(bytes.Clone(last.text), '\n')
	result[len(result)-1] = last

	return result
}

// sides holds parallel removed/added segment lists built during backtracking.
type sides struct {
	removed []segment
	added   []segment
}

// op is a single edit operation produced during LCS backtracking.
type op struct {
	r    rune
	kind byte // 'e' equal, 'd' delete, 'i' insert
}

// lcsSegments builds the LCS table and backtracks to produce Equal/Delete/Insert
// edit operations, then merges consecutive same-kind ops into segment runs.
// rCap and aCap are the byte lengths of the removed and added inputs (without
// their trailing newlines) and are used to pre-allocate segment backing buffers.
func lcsSegments(old, newText []rune, rCap, aCap int) sides {
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

	return mergeSegments(ops, rCap, aCap)
}

// similarityRatioScale is the multiplier used in the similarity ratio formula:
// ratio = 2*equalRunes / totalRunes. The factor of 2 ensures that two identical
// lines score 1.0 (each equal rune is counted once on each side).
const similarityRatioScale = 2

// segmentInitCap is the initial capacity for segment slices in mergeSegments.
// Most diff lines produce far fewer than 8 segments; this avoids the first
// few re-allocations on typical input.
const segmentInitCap = 8

// mergeSegments merges consecutive same-kind ops into segment runs.
// rCap and aCap are the byte capacities for the removed and added backing
// buffers; they must equal the byte lengths of the respective core inputs so
// that the buffers never reallocate, keeping all segment text subslices valid.
func mergeSegments(ops []op, rCap, aCap int) sides {
	removedSegs := make([]segment, 0, segmentInitCap)
	addedSegs := make([]segment, 0, segmentInitCap)

	// Pre-allocated backing buffers. Sized to the exact input byte lengths so
	// no reallocation occurs and existing text subslices are never invalidated.
	removedBacking := make([]byte, 0, rCap)
	addedBacking := make([]byte, 0, aCap)

	var buf [utf8.UTFMax]byte
	for _, o := range ops {
		nb := utf8.EncodeRune(buf[:], o.r)
		r := buf[:nb]

		switch o.kind {
		case 'e':
			removedBacking = append(removedBacking, r...)

			if len(removedSegs) > 0 && !removedSegs[len(removedSegs)-1].changed {
				last := &removedSegs[len(removedSegs)-1]
				last.text = last.text[:len(last.text)+nb]
			} else {
				removedSegs = append(
					removedSegs,
					segment{text: removedBacking[len(removedBacking)-nb:], changed: false},
				)
			}

			addedBacking = append(addedBacking, r...)

			if len(addedSegs) > 0 && !addedSegs[len(addedSegs)-1].changed {
				last := &addedSegs[len(addedSegs)-1]
				last.text = last.text[:len(last.text)+nb]
			} else {
				addedSegs = append(addedSegs, segment{text: addedBacking[len(addedBacking)-nb:], changed: false})
			}

		case 'd':
			removedBacking = append(removedBacking, r...)

			if len(removedSegs) > 0 && removedSegs[len(removedSegs)-1].changed {
				last := &removedSegs[len(removedSegs)-1]
				last.text = last.text[:len(last.text)+nb]
			} else {
				removedSegs = append(removedSegs, segment{text: removedBacking[len(removedBacking)-nb:], changed: true})
			}

		case 'i':
			addedBacking = append(addedBacking, r...)

			if len(addedSegs) > 0 && addedSegs[len(addedSegs)-1].changed {
				last := &addedSegs[len(addedSegs)-1]
				last.text = last.text[:len(last.text)+nb]
			} else {
				addedSegs = append(addedSegs, segment{text: addedBacking[len(addedBacking)-nb:], changed: true})
			}

		default:
			// op.kind is always 'e', 'd', or 'i' — lcsSegments is the only producer.
		}
	}

	// Handle empty inputs: produce a single unchanged empty segment so callers
	// always have at least one segment to work with.
	if len(removedSegs) == 0 {
		removedSegs = append(removedSegs, segment{text: []byte{}, changed: false})
	}

	if len(addedSegs) == 0 {
		addedSegs = append(addedSegs, segment{text: []byte{}, changed: false})
	}

	return sides{removed: removedSegs, added: addedSegs}
}
