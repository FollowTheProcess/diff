package diff_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.followtheprocess.codes/diff"
	"golang.org/x/tools/txtar"
)

// TestLines verifies the Lines method returns structured diff lines.
func TestLines(t *testing.T) {
	tests := []struct {
		name    string
		old     []byte
		newText []byte
		oldName string
		newName string
		want    []diff.Line // nil means we expect nil (inputs equal)
	}{
		{
			name:    "nil on equal inputs",
			oldName: "a", newName: "b",
			old:     []byte("same\n"),
			newText: []byte("same\n"),
			want:    nil,
		},
		{
			name:    "basic add and remove",
			oldName: "want", newName: "got",
			old:     []byte("hello\nworld\n"),
			newText: []byte("hello\nearth\n"),
			want: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
				{Kind: diff.KindHeader, Content: []byte("@@ -1,2 +1,2 @@\n")},
				{Kind: diff.KindContext, Content: []byte("hello\n")},
				{Kind: diff.KindRemoved, Content: []byte("world\n")},
				{Kind: diff.KindAdded, Content: []byte("earth\n")},
			},
		},
		{
			name:    "all added",
			oldName: "want", newName: "got",
			old:     []byte(""),
			newText: []byte("new line\n"),
			want: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
				{Kind: diff.KindHeader, Content: []byte("@@ -0,0 +1,1 @@\n")},
				{Kind: diff.KindAdded, Content: []byte("new line\n")},
			},
		},
		{
			name:    "all removed",
			oldName: "want", newName: "got",
			old:     []byte("old line\n"),
			newText: []byte(""),
			want: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
				{Kind: diff.KindHeader, Content: []byte("@@ -1,1 +0,0 @@\n")},
				{Kind: diff.KindRemoved, Content: []byte("old line\n")},
			},
		},
		{
			name:    "compact shifts duplicate removal to second occurrence",
			oldName: "want", newName: "got",
			old:     []byte("a\na\nb\n"),
			newText: []byte("a\nb\n"),
			want: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
				{Kind: diff.KindHeader, Content: []byte("@@ -1,3 +1,2 @@\n")},
				{Kind: diff.KindContext, Content: []byte("a\n")},
				{Kind: diff.KindRemoved, Content: []byte("a\n")},
				{Kind: diff.KindContext, Content: []byte("b\n")},
			},
		},
		{
			name:    "compact shifts duplicate addition to second occurrence",
			oldName: "want", newName: "got",
			old:     []byte("a\nb\n"),
			newText: []byte("a\na\nb\n"),
			want: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
				{Kind: diff.KindHeader, Content: []byte("@@ -1,2 +1,3 @@\n")},
				{Kind: diff.KindContext, Content: []byte("a\n")},
				{Kind: diff.KindAdded, Content: []byte("a\n")},
				{Kind: diff.KindContext, Content: []byte("b\n")},
			},
		},
		{
			name:    "compact does not shift block at end of hunk with no following context",
			oldName: "want", newName: "got",
			old:     []byte("a\nb\n"),
			newText: []byte("a\n"),
			want: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
				{Kind: diff.KindHeader, Content: []byte("@@ -1,2 +1,1 @@\n")},
				{Kind: diff.KindContext, Content: []byte("a\n")},
				{Kind: diff.KindRemoved, Content: []byte("b\n")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff.New(tt.oldName, tt.old, tt.newName, tt.newText).Lines()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("Lines() = %v, want nil", got)
				}

				return
			}

			if len(got) != len(tt.want) {
				t.Fatalf(
					"Lines() returned %d lines, want %d\ngot: %#v\nwant: %#v",
					len(got),
					len(tt.want),
					got,
					tt.want,
				)
			}

			for i, line := range got {
				if line.Kind != tt.want[i].Kind {
					t.Errorf("line[%d].Kind = %v, want %v", i, line.Kind, tt.want[i].Kind)
				}

				if !bytes.Equal(line.Content, tt.want[i].Content) {
					t.Errorf("line[%d].Content = %q, want %q", i, line.Content, tt.want[i].Content)
				}
			}
		})
	}
}

// TestEqual verifies Equal returns true when old and new are identical.
func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		old  []byte
		new  []byte
	}{
		{
			name: "identical byte slices",
			old:  []byte("same\n"),
			new:  []byte("same\n"),
		},
		{
			name: "both empty",
			old:  []byte(""),
			new:  []byte(""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diff.New("a", tt.old, "b", tt.new); !got.Equal() {
				t.Fatal("Equal() = false, want true for identical inputs")
			}
		})
	}
}

// TestString verifies String returns an empty string for equal inputs and a
// non-empty unified diff for differing inputs.
func TestString(t *testing.T) {
	tests := []struct {
		name        string
		wantContain string
		old         []byte
		new         []byte
		wantEmpty   bool
	}{
		{
			name:      "equal inputs produce empty string",
			old:       []byte("same\n"),
			new:       []byte("same\n"),
			wantEmpty: true,
		},
		{
			name:      "both empty inputs produce empty string",
			old:       []byte(""),
			new:       []byte(""),
			wantEmpty: true,
		},
		{
			name:        "differing inputs produce non-empty unified diff",
			old:         []byte("hello\nworld\n"),
			new:         []byte("hello\nearth\n"),
			wantContain: "- world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff.New("a", tt.old, "b", tt.new).String()
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("String() = %q, want empty string for equal inputs", got)
				}

				return
			}

			if !strings.Contains(got, tt.wantContain) {
				t.Fatalf("String() = %q, want it to contain %q", got, tt.wantContain)
			}
		})
	}
}

// Test drives New against the *.txtar files in testdata/.
func Test(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.txtar"))
	if err != nil {
		t.Fatalf("could not glob txtar files: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("no testdata")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			contents, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("could not read %s: %v", file, err)
			}

			contents = bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))

			archive := txtar.Parse(contents)
			if len(archive.Files) != 3 || archive.Files[2].Name != "diff" {
				t.Fatalf("%s: want three files, third named \"diff\", got: %v", file, archive.Files)
			}

			got := diff.New(
				archive.Files[0].Name,
				clean(archive.Files[0].Data),
				archive.Files[1].Name,
				clean(archive.Files[1].Data),
			).String()
			want := string(clean(archive.Files[2].Data))

			if got != want {
				t.Fatalf("%s: have:\n%s\nwant:\n%s\n%s", file,
					got, want, diff.New("have", []byte(got), "want", []byte(want)))
			}
		})
	}
}

// FuzzLines verifies Lines never panics and returns nil iff inputs are equal.
func FuzzLines(f *testing.F) {
	f.Add([]byte(""), []byte(""))
	f.Add([]byte("same\n"), []byte("same\n"))
	f.Add([]byte("hello\nworld\n"), []byte("hello\nearth\n"))
	f.Add([]byte("completely different\n"), []byte("nothing in common\n"))
	f.Add([]byte("a\nb\nc\n"), []byte("a\nd\nc\n"))
	f.Add([]byte("unicode: héllo\n"), []byte("unicode: wörld\n"))
	f.Add([]byte("   \n\t\n"), []byte("   \n\t\n"))

	f.Fuzz(func(t *testing.T, old, newContent []byte) {
		d := diff.New("a", old, "b", newContent)
		if bytes.Equal(old, newContent) {
			if !d.Equal() {
				t.Fatal("Equal() = false for equal inputs")
			}

			if d.Lines() != nil {
				t.Fatal("Lines() = non-nil for equal inputs")
			}
		} else {
			if d.Equal() {
				t.Fatal("Equal() = true for non-equal inputs")
			}

			if d.Lines() == nil {
				t.Fatal("Lines() = nil for non-equal inputs")
			}
		}
	})
}

// BenchmarkLines benchmarks the Lines method using long.txtar as realistic input.
func BenchmarkLines(b *testing.B) {
	contents, err := os.ReadFile(filepath.Join("testdata", "long.txtar"))
	if err != nil {
		b.Fatalf("could not read long.txtar: %v", err)
	}

	archive := txtar.Parse(contents)
	old := clean(archive.Files[0].Data)
	newContent := clean(archive.Files[1].Data)

	b.ResetTimer()

	for b.Loop() {
		diff.New(archive.Files[0].Name, old, archive.Files[1].Name, newContent).Lines()
	}
}

// BenchmarkString benchmarks String using long.txtar as realistic input.
func BenchmarkString(b *testing.B) {
	contents, err := os.ReadFile(filepath.Join("testdata", "long.txtar"))
	if err != nil {
		b.Fatalf("could not read long.txtar: %v", err)
	}

	archive := txtar.Parse(contents)
	old := clean(archive.Files[0].Data)
	newContent := clean(archive.Files[1].Data)

	b.ResetTimer()

	for b.Loop() {
		_ = diff.New(archive.Files[0].Name, old, archive.Files[1].Name, newContent).String()
	}
}

// TestWithContextOption verifies WithContext is accepted and the default (3) is unchanged.
func TestWithContextOption(t *testing.T) {
	tests := []struct {
		name    string
		old     []byte
		newText []byte
		opts    []diff.Option
		wantLen int // expected number of Lines returned
	}{
		{
			// Default context=3: 3 headers + 1 @@ + 2 context (a, b) + 1 removed + 1 added = 8
			name:    "default context unchanged",
			old:     []byte("a\nb\nc\n"),
			newText: []byte("a\nb\nd\n"),
			opts:    nil,
			wantLen: 8,
		},
		{
			// WithContext(0): 3 headers + 1 @@ + 1 removed + 1 added = 6
			name:    "WithContext(0) suppresses context lines",
			old:     []byte("a\nb\nc\n"),
			newText: []byte("a\nb\nd\n"),
			opts:    []diff.Option{diff.WithContext(0)},
			wantLen: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff.New("old", tt.old, "new", tt.newText, tt.opts...).Lines()
			if len(got) != tt.wantLen {
				t.Fatalf("Lines() returned %d lines, want %d\n%#v", len(got), tt.wantLen, got)
			}
		})
	}
}

// TestGroupIntoHunksViaWithContext verifies the extracted groupIntoHunks function
// respects the contextLines setting via the WithContext option.
func TestGroupIntoHunksViaWithContext(t *testing.T) {
	tests := []struct {
		opt         diff.Option
		name        string
		old         []byte
		new         []byte
		wantContext int
	}{
		{
			name:        "default 3 context lines",
			old:         []byte("a\nb\nc\nd\ne\nf\ng\n"),
			new:         []byte("a\nb\nc\nX\ne\nf\ng\n"),
			opt:         diff.WithContext(3),
			wantContext: 6, // 3 before + 3 after the change
		},
		{
			name:        "zero context lines",
			old:         []byte("a\nb\nc\nd\ne\nf\ng\n"),
			new:         []byte("a\nb\nc\nX\ne\nf\ng\n"),
			opt:         diff.WithContext(0),
			wantContext: 0,
		},
		{
			name:        "one context line",
			old:         []byte("a\nb\nc\nd\ne\nf\ng\n"),
			new:         []byte("a\nb\nc\nX\ne\nf\ng\n"),
			opt:         diff.WithContext(1),
			wantContext: 2, // 1 before + 1 after
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := diff.New("old", tt.old, "new", tt.new, tt.opt).Lines()

			contextCount := 0

			for _, l := range lines {
				if l.Kind == diff.KindContext {
					contextCount++
				}
			}

			if contextCount != tt.wantContext {
				t.Fatalf("got %d KindContext lines, want %d\n%#v", contextCount, tt.wantContext, lines)
			}
		})
	}
}

// TestLargeCommonRegionsProduceCorrectLineNumbers verifies that inputs with large
// common regions produce correct line numbers and only show contextLines of context
// (not the whole prefix).
func TestLargeCommonRegionsProduceCorrectLineNumbers(t *testing.T) {
	tests := []struct {
		name        string
		old         string
		new         string
		wantHunk    string
		wantContext int
	}{
		{
			// 100-line common prefix, then 1 changed line.
			// Hunk shows lines 98–101 (3 context + 1 change = 4 lines each side).
			name:        "large common prefix — correct line numbers",
			old:         strings.Repeat("line\n", 100) + "old content\n",
			new:         strings.Repeat("line\n", 100) + "new content\n",
			wantContext: 3,
			wantHunk:    "@@ -98,4 +98,4 @@",
		},
		{
			// 1 changed line at the start, then 100-line common suffix.
			name:        "large common suffix — correct line numbers",
			old:         "old content\n" + strings.Repeat("line\n", 100),
			new:         "new content\n" + strings.Repeat("line\n", 100),
			wantContext: 3,
			wantHunk:    "@@ -1,4 +1,4 @@",
		},
		{
			// 50 common prefix + 1 change + 50 common suffix.
			name:        "common prefix and suffix",
			old:         strings.Repeat("line\n", 50) + "old\n" + strings.Repeat("line\n", 50),
			new:         strings.Repeat("line\n", 50) + "new\n" + strings.Repeat("line\n", 50),
			wantContext: 6, // 3 before + 3 after
			wantHunk:    "@@ -48,7 +48,7 @@",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := diff.New("old", []byte(tt.old), "new", []byte(tt.new)).Lines()

			contextCount := 0

			var hunkHeader string

			for _, l := range lines {
				if l.Kind == diff.KindContext {
					contextCount++
				}

				if l.Kind == diff.KindHeader && len(l.Content) > 2 && l.Content[0] == '@' {
					hunkHeader = strings.TrimSuffix(string(l.Content), "\n")
				}
			}

			if contextCount != tt.wantContext {
				t.Errorf("context line count = %d, want %d", contextCount, tt.wantContext)
			}

			if hunkHeader != tt.wantHunk {
				t.Errorf("hunk header = %q, want %q", hunkHeader, tt.wantHunk)
			}
		})
	}
}

// TestDisjointFastPath verifies that large fully-disjoint inputs produce the
// correct all-removed / all-added diff output (same as the normal TGS path).
func TestDisjointFastPath(t *testing.T) {
	// Build two 512-line files with no lines in common.
	var oldLines, newLines strings.Builder
	for i := range 512 {
		fmt.Fprintf(&oldLines, "old line %d\n", i)
		fmt.Fprintf(&newLines, "new line %d\n", i)
	}

	old := []byte(oldLines.String())
	newText := []byte(newLines.String())

	d := diff.New("old", old, "new", newText)
	if d.Equal() {
		t.Fatal("Equal() = true for non-equal inputs")
	}

	lines := d.Lines()
	if lines == nil {
		t.Fatal("Lines() returned nil for non-equal inputs")
	}

	removed := 0
	added := 0

	for _, l := range lines {
		switch l.Kind {
		case diff.KindRemoved:
			removed++
		case diff.KindAdded:
			added++
		default:
			// KindHeader and KindContext lines are not counted
		}
	}

	if removed != 512 {
		t.Errorf("removed line count = %d, want 512", removed)
	}

	if added != 512 {
		t.Errorf("added line count = %d, want 512", added)
	}
}

func clean(text []byte) []byte {
	text = bytes.ReplaceAll(text, []byte("$\n"), []byte("\n"))
	text = bytes.TrimSuffix(text, []byte("^D\n"))

	return text
}
