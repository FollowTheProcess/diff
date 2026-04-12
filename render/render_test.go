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
		name string
		old  string
		new  string
	}{
		{
			// Equal inputs should produce nil output — nothing to render.
			name: "equal inputs returns nil",
			old:  "same\n",
			new:  "same\n",
		},
		{
			// Single line change: char-level inline highlighting shows changed characters.
			name: "single line change uses inline char diff",
			old:  "hello world\n",
			new:  "hello earth\n",
		},
		{
			// Two changed lines paired 1:1: each pair gets its own inline diff.
			name: "two paired lines use inline char diff",
			old:  "foo bar\nbaz qux\n",
			new:  "foo baz\nbaz quux\n",
		},
		{
			// Pure deletion: all lines removed, none added.
			name: "pure deletion uses whole-line colour",
			old:  "old line\n",
			new:  "",
		},
		{
			// Pure insertion: all lines added, none removed.
			name: "pure insertion uses whole-line colour",
			old:  "",
			new:  "new line\n",
		},
		{
			// More removed than added: mismatched counts fall back to whole-line colour.
			name: "mismatched count uses whole-line colour",
			old:  "line one\nline two\n",
			new:  "replacement\n",
		},
		{
			// Unicode content: char diff should handle multi-byte runes correctly.
			name: "unicode content",
			old:  "Héllo, wörld!\n",
			new:  "Héllo, wörld! Ünïcödé.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := diff.New("want", []byte(tt.old), "got", []byte(tt.new))
			got := render.Render(d)
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
			d := diff.New("want", []byte(sc.old), "got", []byte(sc.new))
			t.Logf("\n=== %s ===\n%s\n", sc.name, render.Render(d))
		})
	}
}

// BenchmarkRender benchmarks Render using a realistic diff.
func BenchmarkRender(b *testing.B) {
	old := []byte("the quick brown fox\njumps over the lazy dog\nsome context\nmore context\n")
	newContent := []byte("the quick brown cat\njumps over the lazy frog\nsome context\nmore context\n")
	d := diff.New("want", old, "got", newContent)

	b.ResetTimer()

	for b.Loop() {
		render.Render(d)
	}
}
