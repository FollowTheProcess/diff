package render_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"go.followtheprocess.codes/diff"
	"go.followtheprocess.codes/diff/render"
	"go.followtheprocess.codes/hue"
)

var update = flag.Bool("update", false, "update golden files")

func TestMain(m *testing.M) {
	// Force colour on so rendered output is predictable in tests regardless of
	// whether the test runner is attached to a terminal.
	hue.Enabled(true)
	m.Run()
}

func TestRender(t *testing.T) {
	tests := []struct {
		name  string
		lines []diff.Line
	}{
		{
			name:  "nil input returns empty string",
			lines: nil,
		},
		{
			name:  "empty slice returns empty string",
			lines: []diff.Line{},
		},
		{
			name: "context line has no colour and double-space prefix",
			lines: []diff.Line{
				{Kind: diff.KindContext, Content: []byte("unchanged\n")},
			},
		},
		{
			name: "diff header line is bold no colour",
			lines: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("diff want got\n")},
			},
		},
		{
			name: "removed header line is red",
			lines: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("--- want\n")},
			},
		},
		{
			name: "added header line is green",
			lines: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("+++ got\n")},
			},
		},
		{
			name: "hunk header is bold no colour",
			lines: []diff.Line{
				{Kind: diff.KindHeader, Content: []byte("@@ -1,1 +1,1 @@\n")},
			},
		},
		{
			name: "standalone removed line has red prefix and content",
			lines: []diff.Line{
				{Kind: diff.KindRemoved, Content: []byte("old line\n")},
			},
		},
		{
			name: "standalone added line has green prefix and content",
			lines: []diff.Line{
				{Kind: diff.KindAdded, Content: []byte("new line\n")},
			},
		},
		{
			// "old" and "new" share no characters, so CharDiff produces a single
			// Changed segment per side with the \n reattached as a separate unchanged segment.
			name: "matched removed added pair uses inline char diff with coloured prefixes",
			lines: []diff.Line{
				{Kind: diff.KindRemoved, Content: []byte("old\n")},
				{Kind: diff.KindAdded, Content: []byte("new\n")},
			},
		},
		{
			name: "mismatched count uses whole-line colour with coloured prefixes",
			lines: []diff.Line{
				{Kind: diff.KindRemoved, Content: []byte("line one\n")},
				{Kind: diff.KindRemoved, Content: []byte("line two\n")},
				{Kind: diff.KindAdded, Content: []byte("replacement\n")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render.Render(tt.lines)
			golden := filepath.Join("testdata", filepath.FromSlash(t.Name())+".txt")

			if *update {
				err := os.MkdirAll(filepath.Dir(golden), 0o755)
				if err != nil {
					t.Fatalf("create golden dir: %v", err)
				}

				err = os.WriteFile(golden, got, 0o644)
				if err != nil {
					t.Fatalf("update golden: %v", err)
				}

				return
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			if !bytes.Equal(got, want) {
				t.Errorf("Render() =\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// TestVisualDiff is a manual smoke-check for the diff renderer.
// Run with go test -v to see the colourised output in your terminal.
func TestVisualDiff(t *testing.T) {
	scenarios := []struct {
		name string
		old  string
		new  string
	}{
		{
			// Single changed line: char-level inline highlighting should show
			// exactly which characters differ.
			name: "single line change (inline char diff)",
			old: `func greet(name string) string {
	return "Hello, " + name
}
`,
			new: `func greet(name string) string {
	return "Hello, " + name + "!"
}
`,
		},
		{
			// Two changed lines paired 1:1: each pair gets its own inline diff.
			name: "multi-line paired change",
			old: `func (s *Server) Start(port int) error {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	return http.ListenAndServe(addr, s.mux)
}
`,
			new: `func (s *Server) Start(ctx context.Context, port int) error {
	addr := fmt.Sprintf(":%d", port)
	return s.httpServer.ListenAndServeContext(ctx, addr)
}
`,
		},
		{
			// More removed than added: mismatched counts fall back to whole-line colour.
			name: "mismatched counts (whole-line fallback)",
			old: `case "json":
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
`,
			new: `case "json":
	return json.NewEncoder(w).Encode(v)
`,
		},
		{
			// Unicode content: char diff should handle multi-byte runes correctly.
			name: "unicode content",
			old:  "Héllo, wörld! Ünïcödé is fün.\n",
			new:  "Héllo, wörld! Ünïcödé is grëat.\n",
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			lines := diff.Lines("want", []byte(sc.old), "got", []byte(sc.new))
			t.Logf("\n=== %s ===\n%s\n", sc.name, render.Render(lines))
		})
	}
}

// BenchmarkRender benchmarks Render using a realistic diff.
func BenchmarkRender(b *testing.B) {
	old := []byte("the quick brown fox\njumps over the lazy dog\nsome context\nmore context\n")
	newContent := []byte("the quick brown cat\njumps over the lazy frog\nsome context\nmore context\n")
	lines := diff.Lines("want", old, "got", newContent)

	b.ResetTimer()

	for b.Loop() {
		render.Render(lines)
	}
}
