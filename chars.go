package diff

import (
	"bytes"
	"unicode"
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
//   - If the similarity ratio (2*equalRunes / totalRunes) is below the threshold
//     set by [WithSimilarityThreshold] (default 0.5), a whole-line Changed segment
//     is returned — intraline highlighting of very different lines is noisy.
//
// The concatenation of segment Text fields on each side always equals the
// original input byte-for-byte.
func CharDiff(removed, added []byte, opts ...Option) InlineChange {
	cfg := applyOptions(opts)

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

	segs := lcsSegments(oldRunes, newRunes, len(removedCore), len(addedCore))

	// Similarity ratio gate: if the ratio of equal runes to total runes is below
	// the threshold, intraline highlighting would be more noise than signal.
	// Build the whole-line fallback using the stripped core slices so that
	// reattachNL can place the trailing newline outside the Changed segment —
	// a highlighted \n bleeds ANSI background colour onto the next terminal line.
	totalRunes := len(oldRunes) + len(newRunes)
	if totalRunes > 0 {
		equalRunes := 0

		for _, seg := range segs.removed {
			if !seg.Changed {
				equalRunes += utf8.RuneCount(seg.Text)
			}
		}

		ratio := float64(similarityRatioScale*equalRunes) / float64(totalRunes)
		if ratio < cfg.similarityThreshold {
			var rSegs []Segment
			if len(removedCore) > 0 {
				rSegs = []Segment{{Text: bytes.Clone(removedCore), Changed: true}}
			}

			var aSegs []Segment
			if len(addedCore) > 0 {
				aSegs = []Segment{{Text: bytes.Clone(addedCore), Changed: true}}
			}

			return InlineChange{
				Removed: reattachNL(rSegs, removedNL),
				Added:   reattachNL(aSegs, addedNL),
			}
		}
	}

	removedSegs := reattachNL(segs.removed, removedNL)
	addedSegs := reattachNL(segs.added, addedNL)

	return InlineChange{Removed: removedSegs, Added: addedSegs}
}

// tokenize splits b into word and delimiter tokens.
// Words are maximal runs of Unicode letters, digits, and underscores.
// Every other rune is an individual delimiter token.
// Tokens are subslices of b — no allocation beyond the slice header list.
func tokenize(b []byte) [][]byte {
	var tokens [][]byte

	i := 0
	for i < len(b) {
		r, sz := utf8.DecodeRune(b[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			start := i
			for i < len(b) {
				r, sz = utf8.DecodeRune(b[i:])
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
					break
				}

				i += sz
			}

			tokens = append(tokens, b[start:i])
		} else {
			tokens = append(tokens, b[i:i+sz])
			i += sz
		}
	}

	return tokens
}

// tokenOp is a single edit operation produced during word-token LCS backtracking.
type tokenOp struct {
	tok  []byte
	kind byte // 'e' equal, 'd' delete, 'i' insert
}

// wordLCSSegments builds the LCS table for token slices and returns Segment-based sides.
// It is the word-token analogue of lcsSegments: tokens replace runes, bytes.Equal
// replaces rune equality, and each token's bytes are appended to the backing buffers.
// rCap and aCap must equal the byte lengths of the removed and added core inputs so
// the backing buffers never reallocate.
func wordLCSSegments(old, newToks [][]byte, rCap, aCap int) sides {
	m, n := len(old), len(newToks)

	dp := make([]int, (m+1)*(n+1))

	stride := n + 1
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if bytes.Equal(old[i-1], newToks[j-1]) {
				dp[i*stride+j] = dp[(i-1)*stride+(j-1)] + 1
			} else {
				dp[i*stride+j] = max(dp[(i-1)*stride+j], dp[i*stride+(j-1)])
			}
		}
	}

	ops := make([]tokenOp, 0, m+n)

	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && bytes.Equal(old[i-1], newToks[j-1]):
			ops = append(ops, tokenOp{tok: old[i-1], kind: 'e'})
			i--
			j--
		case j > 0 && (i == 0 || dp[i*stride+(j-1)] >= dp[(i-1)*stride+j]):
			ops = append(ops, tokenOp{tok: newToks[j-1], kind: 'i'})
			j--
		default:
			ops = append(ops, tokenOp{tok: old[i-1], kind: 'd'})
			i--
		}
	}

	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	return mergeTokenSegments(ops, rCap, aCap)
}

// mergeTokenSegments merges consecutive same-kind token ops into Segment runs.
// rCap and aCap are the byte capacities for the removed and added backing buffers;
// they must equal the byte lengths of the respective core inputs so the buffers
// never reallocate, keeping all segment Text subslices valid.
func mergeTokenSegments(ops []tokenOp, rCap, aCap int) sides {
	removedBacking := make([]byte, 0, rCap)
	addedBacking := make([]byte, 0, aCap)
	removedSegs := make([]Segment, 0, segmentInitCap)
	addedSegs := make([]Segment, 0, segmentInitCap)

	for _, o := range ops {
		nb := len(o.tok)

		switch o.kind {
		case 'e':
			removedBacking = append(removedBacking, o.tok...)

			if len(removedSegs) > 0 && !removedSegs[len(removedSegs)-1].Changed {
				last := &removedSegs[len(removedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				removedSegs = append(
					removedSegs,
					Segment{Text: removedBacking[len(removedBacking)-nb:], Changed: false},
				)
			}

			addedBacking = append(addedBacking, o.tok...)

			if len(addedSegs) > 0 && !addedSegs[len(addedSegs)-1].Changed {
				last := &addedSegs[len(addedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				addedSegs = append(addedSegs, Segment{Text: addedBacking[len(addedBacking)-nb:], Changed: false})
			}

		case 'd':
			removedBacking = append(removedBacking, o.tok...)

			if len(removedSegs) > 0 && removedSegs[len(removedSegs)-1].Changed {
				last := &removedSegs[len(removedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				removedSegs = append(removedSegs, Segment{Text: removedBacking[len(removedBacking)-nb:], Changed: true})
			}

		case 'i':
			addedBacking = append(addedBacking, o.tok...)

			if len(addedSegs) > 0 && addedSegs[len(addedSegs)-1].Changed {
				last := &addedSegs[len(addedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				addedSegs = append(addedSegs, Segment{Text: addedBacking[len(addedBacking)-nb:], Changed: true})
			}

		default:
			// op.kind is always 'e', 'd', or 'i' — wordLCSSegments is the only producer.
		}
	}

	if len(removedSegs) == 0 {
		removedSegs = append(removedSegs, Segment{Text: []byte{}, Changed: false})
	}

	if len(addedSegs) == 0 {
		addedSegs = append(addedSegs, Segment{Text: []byte{}, Changed: false})
	}

	return sides{removed: removedSegs, added: addedSegs}
}

// WordDiff computes word-level diff segments for a removed/added line pair.
// It tokenises each line into words (maximal runs of Unicode letters, digits,
// and underscores) and delimiters (all other runes, individually), then runs
// an LCS on the token slices. Changed segments cover whole words or delimiter
// sequences, producing wider, cleaner highlights than [CharDiff] for prose or
// code identifiers.
//
// The same guards as [CharDiff] apply:
//   - Identical inputs return all-unchanged segments.
//   - Trailing newlines are stripped before diffing and reattached afterwards;
//     a trailing newline is never placed inside a Changed segment.
//   - Invalid UTF-8 returns a whole-line Changed segment.
//   - Either side exceeding 500 tokens returns a whole-line Changed segment.
//   - If the similarity ratio (2*equalBytes / totalBytes) is below the threshold
//     set by [WithSimilarityThreshold] (default 0.5), a whole-line Changed
//     segment is returned.
//
// The concatenation of segment Text fields on each side always equals the
// original input byte-for-byte.
func WordDiff(removed, added []byte, opts ...Option) InlineChange {
	cfg := applyOptions(opts)

	if bytes.Equal(removed, added) {
		return InlineChange{
			Removed: []Segment{{Text: bytes.Clone(removed), Changed: false}},
			Added:   []Segment{{Text: bytes.Clone(added), Changed: false}},
		}
	}

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

	if !utf8.Valid(removedCore) || !utf8.Valid(addedCore) {
		return fallback(removed, added)
	}

	oldToks := tokenize(removedCore)
	newToks := tokenize(addedCore)

	if len(oldToks) > 500 || len(newToks) > 500 {
		return fallback(removed, added)
	}

	segs := wordLCSSegments(oldToks, newToks, len(removedCore), len(addedCore))

	// Similarity ratio gate using byte counts for consistency with CharDiff.
	totalBytes := len(removedCore) + len(addedCore)
	if totalBytes > 0 {
		equalBytes := 0

		for _, seg := range segs.removed {
			if !seg.Changed {
				equalBytes += len(seg.Text)
			}
		}

		ratio := float64(similarityRatioScale*equalBytes) / float64(totalBytes)
		if ratio < cfg.similarityThreshold {
			var rSegs []Segment
			if len(removedCore) > 0 {
				rSegs = []Segment{{Text: bytes.Clone(removedCore), Changed: true}}
			}

			var aSegs []Segment
			if len(addedCore) > 0 {
				aSegs = []Segment{{Text: bytes.Clone(addedCore), Changed: true}}
			}

			return InlineChange{
				Removed: reattachNL(rSegs, removedNL),
				Added:   reattachNL(aSegs, addedNL),
			}
		}
	}

	return InlineChange{
		Removed: reattachNL(segs.removed, removedNL),
		Added:   reattachNL(segs.added, addedNL),
	}
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

// mergeSegments merges consecutive same-kind ops into Segment runs.
// rCap and aCap are the byte capacities for the removed and added backing
// buffers; they must equal the byte lengths of the respective core inputs so
// that the buffers never reallocate, keeping all segment Text subslices valid.
func mergeSegments(ops []op, rCap, aCap int) sides {
	removedSegs := make([]Segment, 0, segmentInitCap)
	addedSegs := make([]Segment, 0, segmentInitCap)

	// Pre-allocated backing buffers. Sized to the exact input byte lengths so
	// no reallocation occurs and existing Text subslices are never invalidated.
	removedBacking := make([]byte, 0, rCap)
	addedBacking := make([]byte, 0, aCap)

	var buf [utf8.UTFMax]byte
	for _, o := range ops {
		nb := utf8.EncodeRune(buf[:], o.r)
		r := buf[:nb]

		switch o.kind {
		case 'e':
			removedBacking = append(removedBacking, r...)

			if len(removedSegs) > 0 && !removedSegs[len(removedSegs)-1].Changed {
				last := &removedSegs[len(removedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				removedSegs = append(
					removedSegs,
					Segment{Text: removedBacking[len(removedBacking)-nb:], Changed: false},
				)
			}

			addedBacking = append(addedBacking, r...)

			if len(addedSegs) > 0 && !addedSegs[len(addedSegs)-1].Changed {
				last := &addedSegs[len(addedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				addedSegs = append(addedSegs, Segment{Text: addedBacking[len(addedBacking)-nb:], Changed: false})
			}

		case 'd':
			removedBacking = append(removedBacking, r...)

			if len(removedSegs) > 0 && removedSegs[len(removedSegs)-1].Changed {
				last := &removedSegs[len(removedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				removedSegs = append(removedSegs, Segment{Text: removedBacking[len(removedBacking)-nb:], Changed: true})
			}

		case 'i':
			addedBacking = append(addedBacking, r...)

			if len(addedSegs) > 0 && addedSegs[len(addedSegs)-1].Changed {
				last := &addedSegs[len(addedSegs)-1]
				last.Text = last.Text[:len(last.Text)+nb]
			} else {
				addedSegs = append(addedSegs, Segment{Text: addedBacking[len(addedBacking)-nb:], Changed: true})
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
