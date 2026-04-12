package diff_test

import (
	"bytes"
	"strings"
	"testing"

	"go.followtheprocess.codes/diff"
)

func TestCharDiff(t *testing.T) {
	tests := []struct {
		name             string
		removed          []byte
		added            []byte
		wantAllUnchanged bool // if true, expect all segments Changed:false (identical inputs)
		wantHasChanged   bool // if true, expect at least one Changed:true segment on either side
	}{
		{
			name:             "identical lines - all segments unchanged",
			removed:          []byte("hello world\n"),
			added:            []byte("hello world\n"),
			wantAllUnchanged: true,
		},
		{
			name:           "completely different",
			removed:        []byte("abc\n"),
			added:          []byte("xyz\n"),
			wantHasChanged: true,
		},
		{
			name:           "prefix change",
			removed:        []byte("foobar\n"),
			added:          []byte("bazbar\n"),
			wantHasChanged: true,
		},
		{
			name:           "suffix change",
			removed:        []byte("hello world\n"),
			added:          []byte("hello earth\n"),
			wantHasChanged: true,
		},
		{
			name:           "middle change",
			removed:        []byte("hello world bye\n"),
			added:          []byte("hello earth bye\n"),
			wantHasChanged: true,
		},
		{
			name:           "unicode",
			removed:        []byte("héllo wörld\n"),
			added:          []byte("héllo earth\n"),
			wantHasChanged: true,
		},
		{
			name:           "empty removed",
			removed:        []byte(""),
			added:          []byte("new content\n"),
			wantHasChanged: true,
		},
		{
			name:           "empty added",
			removed:        []byte("old content\n"),
			added:          []byte(""),
			wantHasChanged: true,
		},
		{
			name:             "both empty",
			removed:          []byte(""),
			added:            []byte(""),
			wantAllUnchanged: true,
		},
		{
			name:           "trailing newline preserved",
			removed:        []byte("line\n"),
			added:          []byte("changed\n"),
			wantHasChanged: true,
		},
		{
			// Regression: invalid UTF-8 on the added side must not corrupt the join invariant.
			// \xe2 is the start of a 3-byte sequence with no continuation bytes.
			name:           "invalid UTF-8 in added side",
			removed:        []byte("0"),
			added:          []byte("\xe2"),
			wantHasChanged: true,
		},
		{
			// Regression: invalid UTF-8 on the removed side.
			name:           "invalid UTF-8 in removed side",
			removed:        []byte("\xe2"),
			added:          []byte("0"),
			wantHasChanged: true,
		},
		{
			// Regression: identical invalid UTF-8 bytes must produce all-unchanged segments.
			// \x80 is a bare continuation byte — invalid UTF-8 on its own.
			name:             "identical invalid UTF-8",
			removed:          []byte("\x80"),
			added:            []byte("\x80"),
			wantAllUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := diff.CharDiff(tt.removed, tt.added)

			assertJoinInvariant(t, result, tt.removed, tt.added)

			if tt.wantAllUnchanged {
				assertNoneChanged(t, result.Removed, "removed")
				assertNoneChanged(t, result.Added, "added")
			}

			if tt.wantHasChanged {
				if !anyChanged(result.Removed) && !anyChanged(result.Added) {
					t.Error("CharDiff on differing inputs should produce at least one Changed segment")
				}
			}
		})
	}
}

// TestCharDiffCapTriggers verifies the >500 rune safety cap produces a single-segment fallback.
func TestCharDiffCapTriggers(t *testing.T) {
	long := []byte(strings.Repeat("a", 501) + "\n")
	other := []byte(strings.Repeat("b", 501) + "\n")

	result := diff.CharDiff(long, other)

	if len(result.Removed) != 1 {
		t.Errorf("expected 1 removed segment for >500 rune input, got %d", len(result.Removed))
	}

	if len(result.Added) != 1 {
		t.Errorf("expected 1 added segment for >500 rune input, got %d", len(result.Added))
	}

	if !result.Removed[0].Changed {
		t.Error("expected removed fallback segment to be Changed:true")
	}

	if !result.Added[0].Changed {
		t.Error("expected added fallback segment to be Changed:true")
	}
}

// TestCharDiffNewlineNotHighlighted asserts that the trailing newline is never included in a
// Changed segment. A highlighted \n causes the ANSI background colour to bleed onto the next
// terminal line when rendered.
func TestCharDiffNewlineNotHighlighted(t *testing.T) {
	tests := []struct {
		name    string
		removed []byte
		added   []byte
	}{
		{
			name:    "suffix added",
			removed: []byte("hello\n"),
			added:   []byte("hello world\n"),
		},
		{
			name:    "inline change with trailing newline",
			removed: []byte("\treturn \"Hello, \" + name\n"),
			added:   []byte("\treturn \"Hello, \" + name + \"!\"\n"),
		},
		{
			name:    "completely different lines",
			removed: []byte("abc\n"),
			added:   []byte("xyz\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := diff.CharDiff(tt.removed, tt.added)

			for i, seg := range result.Removed {
				if seg.Changed && len(seg.Text) > 0 && seg.Text[len(seg.Text)-1] == '\n' {
					t.Errorf(
						"removed segment[%d] is Changed=true but ends with \\n; highlight would bleed onto next terminal line",
						i,
					)
				}
			}

			for i, seg := range result.Added {
				if seg.Changed && len(seg.Text) > 0 && seg.Text[len(seg.Text)-1] == '\n' {
					t.Errorf(
						"added segment[%d] is Changed=true but ends with \\n; highlight would bleed onto next terminal line",
						i,
					)
				}
			}
		})
	}
}

// TestCharDiffNotSameForDifferent verifies that differing inputs produce at least one Changed segment.
func TestCharDiffNotSameForDifferent(t *testing.T) {
	result := diff.CharDiff([]byte("hello\n"), []byte("world\n"))

	hasChanged := false

	for _, seg := range result.Removed {
		if seg.Changed {
			hasChanged = true
			break
		}
	}

	if !hasChanged {
		for _, seg := range result.Added {
			if seg.Changed {
				hasChanged = true
				break
			}
		}
	}

	if !hasChanged {
		t.Error("CharDiff on different inputs should produce at least one Changed segment")
	}
}

// TestCharDiffSimilarityGate verifies that WithSimilarityThreshold suppresses
// intraline highlighting when the ratio of equal runes is below the threshold.
func TestCharDiffSimilarityGate(t *testing.T) {
	tests := []struct {
		name             string
		removed          []byte
		added            []byte
		threshold        float64
		wantAllChanged   bool // true = expect single whole-line Changed segment (fallback)
		wantHasUnchanged bool // true = expect at least one unchanged (intraline) segment
	}{
		{
			// "func Foo() {" vs "type Bar struct {" share almost nothing.
			// Default threshold 0.5 should suppress highlighting.
			name:           "dissimilar lines suppressed at default threshold",
			removed:        []byte("func Foo() {\n"),
			added:          []byte("type Bar struct {\n"),
			threshold:      0.5,
			wantAllChanged: true,
		},
		{
			// Same pair, but threshold=0 means always highlight.
			name:             "dissimilar lines highlighted at threshold=0",
			removed:          []byte("func Foo() {\n"),
			added:            []byte("type Bar struct {\n"),
			threshold:        0.0,
			wantHasUnchanged: true, // " {" is common
		},
		{
			// Highly similar lines should always produce intraline highlights.
			name:             "similar lines produce intraline highlighting",
			removed:          []byte("return fmt.Errorf(\"could not open file: %w\", err)\n"),
			added:            []byte("return fmt.Errorf(\"failed to read config: %w\", err)\n"),
			threshold:        0.5,
			wantHasUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := diff.CharDiff(tt.removed, tt.added, diff.WithSimilarityThreshold(tt.threshold))

			assertJoinInvariant(t, result, tt.removed, tt.added)

			if tt.wantAllChanged {
				assertAllChangedExceptNL(t, result.Removed, "removed")
				assertAllChangedExceptNL(t, result.Added, "added")
			}

			if tt.wantHasUnchanged {
				assertHasUnchanged(t, result.Removed)
			}
		})
	}
}

// BenchmarkCharDiff benchmarks the CharDiff function.
func BenchmarkCharDiff(b *testing.B) {
	removed := []byte("the quick brown fox jumps over the lazy dog\n")
	added := []byte("the quick brown cat jumps over the lazy frog\n")

	b.ResetTimer()

	for b.Loop() {
		diff.CharDiff(removed, added)
	}
}

// FuzzCharDiff verifies CharDiff never panics, terminates, and produces segments
// whose concatenation equals the original input on each side.
func FuzzCharDiff(f *testing.F) {
	f.Add([]byte("hello\n"), []byte("hello\n"))
	f.Add([]byte("a\n"), []byte("b\n"))
	f.Add([]byte("the quick brown fox\n"), []byte("the quick brown cat\n"))
	f.Add([]byte(strings.Repeat("a", 10)+"\n"), []byte(strings.Repeat("b", 10)+"\n"))
	f.Add([]byte("héllo\n"), []byte("wörld\n"))
	f.Add([]byte(""), []byte("content\n"))
	f.Add([]byte("content\n"), []byte(""))

	f.Fuzz(func(t *testing.T, removed, added []byte) {
		result := diff.CharDiff(removed, added)

		if joinSegments(result.Removed) != string(removed) {
			t.Fatalf("removed segments join = %q, want %q", joinSegments(result.Removed), string(removed))
		}

		if joinSegments(result.Added) != string(added) {
			t.Fatalf("added segments join = %q, want %q", joinSegments(result.Added), string(added))
		}

		if string(removed) == string(added) {
			for i, seg := range result.Removed {
				if seg.Changed {
					t.Fatalf("removed segment[%d] Changed=true for identical inputs", i)
				}
			}

			for i, seg := range result.Added {
				if seg.Changed {
					t.Fatalf("added segment[%d] Changed=true for identical inputs", i)
				}
			}
		}
	})
}

func assertJoinInvariant(t *testing.T, result diff.InlineChange, removed, added []byte) {
	t.Helper()

	if got := joinSegments(result.Removed); got != string(removed) {
		t.Errorf("removed segments join = %q, want %q", got, string(removed))
	}

	if got := joinSegments(result.Added); got != string(added) {
		t.Errorf("added segments join = %q, want %q", got, string(added))
	}
}

func assertNoneChanged(t *testing.T, segs []diff.Segment, side string) {
	t.Helper()

	for i, seg := range segs {
		if seg.Changed {
			t.Errorf("%s segment[%d] Changed=true, want false for identical inputs", side, i)
		}
	}
}

func anyChanged(segs []diff.Segment) bool {
	for _, s := range segs {
		if s.Changed {
			return true
		}
	}

	return false
}

// assertAllChangedExceptNL asserts that all segments are Changed=true, except for a
// trailing standalone newline segment which is always Changed=false (to prevent ANSI
// background colours from bleeding onto the next terminal line).
func assertAllChangedExceptNL(t *testing.T, segs []diff.Segment, side string) {
	t.Helper()

	for i, seg := range segs {
		isTrailingNL := len(seg.Text) == 1 && seg.Text[0] == '\n'
		if !seg.Changed && !isTrailingNL {
			t.Errorf("%s segment[%d] Changed=false, want whole-line fallback", side, i)
		}
	}
}

// assertHasUnchanged asserts that at least one segment in segs has Changed=false,
// meaning intraline highlighting found common content.
func assertHasUnchanged(t *testing.T, segs []diff.Segment) {
	t.Helper()

	for _, seg := range segs {
		if !seg.Changed {
			return
		}
	}

	t.Error("expected at least one unchanged segment for similar lines")
}

func joinSegments(segs []diff.Segment) string {
	var sb strings.Builder
	for _, s := range segs {
		sb.Write(s.Text)
	}

	return sb.String()
}

func TestWordDiff(t *testing.T) {
	tests := []struct {
		name             string
		removed          []byte
		added            []byte
		wantAllUnchanged bool
		wantHasChanged   bool
	}{
		{
			name:             "identical lines - all segments unchanged",
			removed:          []byte("return foo\n"),
			added:            []byte("return foo\n"),
			wantAllUnchanged: true,
		},
		{
			name:           "changed word - only the word is highlighted",
			removed:        []byte("return foo\n"),
			added:          []byte("return bar\n"),
			wantHasChanged: true,
		},
		{
			name:           "completely different lines",
			removed:        []byte("abc def\n"),
			added:          []byte("xyz qrs\n"),
			wantHasChanged: true,
		},
		{
			name:           "empty removed",
			removed:        []byte(""),
			added:          []byte("new content\n"),
			wantHasChanged: true,
		},
		{
			name:           "invalid UTF-8 falls back to whole-line",
			removed:        []byte("\xff\xfe old\n"),
			added:          []byte("new content\n"),
			wantHasChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := diff.WordDiff(tt.removed, tt.added)

			assertJoinInvariant(t, result, tt.removed, tt.added)

			if tt.wantAllUnchanged {
				assertNoneChanged(t, result.Removed, "removed")
				assertNoneChanged(t, result.Added, "added")
			}

			if tt.wantHasChanged {
				if !anyChanged(result.Removed) && !anyChanged(result.Added) {
					t.Error("WordDiff on differing inputs should produce at least one Changed segment")
				}
			}
		})
	}
}

func FuzzWordDiff(f *testing.F) {
	f.Add([]byte("return foo\n"), []byte("return bar\n"))
	f.Add([]byte("hello world\n"), []byte("hello earth\n"))
	f.Add([]byte(""), []byte(""))

	f.Fuzz(func(t *testing.T, removed, added []byte) {
		got := diff.WordDiff(removed, added)

		var rConcat []byte
		for _, s := range got.Removed {
			rConcat = append(rConcat, s.Text...)
		}

		if !bytes.Equal(rConcat, removed) {
			t.Fatalf("Removed concat %q != input %q", rConcat, removed)
		}

		var aConcat []byte
		for _, s := range got.Added {
			aConcat = append(aConcat, s.Text...)
		}

		if !bytes.Equal(aConcat, added) {
			t.Fatalf("Added concat %q != input %q", aConcat, added)
		}
	})
}
