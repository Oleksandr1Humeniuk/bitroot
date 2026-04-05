package main

import (
	"strings"
	"testing"

	"bitroot/internal/scanner"
	"bitroot/internal/storage"
)

func TestBuildRAGContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []storage.SearchResult
		want    []string
	}{
		{
			name:    "empty results",
			results: nil,
			want:    []string{"(no semantic context)"},
		},
		{
			name: "renders ranked context",
			results: []storage.SearchResult{
				{Path: "internal/scanner/chunker.go", Score: 0.92, Summary: "Chunking strategy for source files.", Refs: []string{"internal/scanner/chunker.go:10-30"}},
				{Path: "internal/storage/storage.go", Score: 0.88, Summary: "Vector search and persistence.", Refs: []string{"internal/storage/storage.go:120-165"}},
			},
			want: []string{
				"[1] Path: internal/scanner/chunker.go",
				"Score: 0.9200",
				"Line references: internal/scanner/chunker.go:10-30",
				"Summary: Chunking strategy for source files.",
				"[2] Path: internal/storage/storage.go",
				"Score: 0.8800",
				"Line references: internal/storage/storage.go:120-165",
				"Summary: Vector search and persistence.",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, sources := buildRAGContext(tt.results)
			for _, expected := range tt.want {
				if !strings.Contains(got, expected) {
					t.Fatalf("context missing %q\nGot:\n%s", expected, got)
				}
			}

			if len(tt.results) != len(sources) {
				t.Fatalf("sources size mismatch: got %d want %d", len(sources), len(tt.results))
			}
		})
	}
}

func TestChunkRefs(t *testing.T) {
	t.Parallel()

	chunks := []scanner.CodeChunk{
		{FilePath: "a.go", StartLine: 1, EndLine: 10},
		{FilePath: "a.go", StartLine: 11, EndLine: 20},
		{FilePath: "a.go", StartLine: 21, EndLine: 30},
	}

	refs := chunkRefs(chunks, 2)
	if len(refs) != 2 {
		t.Fatalf("refs length mismatch: got %d want 2", len(refs))
	}
	if refs[0] != "a.go:1-10" || refs[1] != "a.go:11-20" {
		t.Fatalf("unexpected refs: %#v", refs)
	}
}
