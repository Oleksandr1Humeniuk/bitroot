package main

import (
	"strings"
	"testing"

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
				{Path: "internal/scanner/chunker.go", Score: 0.92, Summary: "Chunking strategy for source files."},
				{Path: "internal/storage/storage.go", Score: 0.88, Summary: "Vector search and persistence."},
			},
			want: []string{
				"[1] Path: internal/scanner/chunker.go",
				"Score: 0.9200",
				"Summary: Chunking strategy for source files.",
				"[2] Path: internal/storage/storage.go",
				"Score: 0.8800",
				"Summary: Vector search and persistence.",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildRAGContext(tt.results)
			for _, expected := range tt.want {
				if !strings.Contains(got, expected) {
					t.Fatalf("context missing %q\nGot:\n%s", expected, got)
				}
			}
		})
	}
}
