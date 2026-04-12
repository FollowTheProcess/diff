package diff_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.followtheprocess.codes/diff"
	"golang.org/x/tools/txtar"
)

// TestLines verifies the Lines function returns structured diff lines.
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff.Lines(tt.oldName, tt.old, tt.newName, tt.newText)
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

// TestDiffNilOnEqual verifies Diff returns nil when old and newText are identical.
func TestDiffNilOnEqual(t *testing.T) {
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
			if got := diff.Diff("a", tt.old, "b", tt.new); got != nil {
				t.Fatalf("Diff() = %q, want nil for equal inputs", got)
			}
		})
	}
}

// Test drives Diff against the *.txtar files in testdata/.
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

			diffs := diff.Diff(
				archive.Files[0].Name,
				clean(archive.Files[0].Data),
				archive.Files[1].Name,
				clean(archive.Files[1].Data),
			)
			want := clean(archive.Files[2].Data)

			if !bytes.Equal(diffs, want) {
				t.Fatalf("%s: have:\n%s\nwant:\n%s\n%s", file,
					diffs, want, diff.Diff("have", diffs, "want", want))
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
		result := diff.Lines("a", old, "b", newContent)
		if bytes.Equal(old, newContent) {
			if result != nil {
				t.Fatal("Lines() = non-nil for equal inputs")
			}
		} else {
			if result == nil {
				t.Fatal("Lines() = nil for non-equal inputs")
			}
		}
	})
}

// BenchmarkLines benchmarks the Lines function using long.txtar as realistic input.
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
		diff.Lines(archive.Files[0].Name, old, archive.Files[1].Name, newContent)
	}
}

// BenchmarkDiff benchmarks the Diff function using long.txtar as realistic input.
func BenchmarkDiff(b *testing.B) {
	contents, err := os.ReadFile(filepath.Join("testdata", "long.txtar"))
	if err != nil {
		b.Fatalf("could not read long.txtar: %v", err)
	}

	archive := txtar.Parse(contents)
	old := clean(archive.Files[0].Data)
	newContent := clean(archive.Files[1].Data)

	b.ResetTimer()

	for b.Loop() {
		diff.Diff(archive.Files[0].Name, old, archive.Files[1].Name, newContent)
	}
}

func clean(text []byte) []byte {
	text = bytes.ReplaceAll(text, []byte("$\n"), []byte("\n"))
	text = bytes.TrimSuffix(text, []byte("^D\n"))

	return text
}
