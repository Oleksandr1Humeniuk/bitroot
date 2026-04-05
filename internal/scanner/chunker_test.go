package scanner_test

import (
	"strings"
	"testing"

	"bitroot/internal/scanner"
)

func TestChunkSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		language     string
		source       string
		opts         scanner.ChunkOptions
		wantCount    int
		wantMinCount int
		wantMax      int
	}{
		{
			name:         "empty source returns no chunks",
			language:     "go",
			source:       "",
			opts:         scanner.DefaultChunkOptions(),
			wantCount:    0,
			wantMinCount: 0,
			wantMax:      0,
		},
		{
			name:     "go functions split by logical boundaries",
			language: "go",
			source: strings.Join([]string{
				"package main",
				"",
				"func alpha() {",
				"\tprintln(\"a\")",
				"}",
				"",
				"func beta() {",
				"\tprintln(\"b\")",
				"}",
				"",
				"func gamma() {",
				"\tprintln(\"c\")",
				"}",
			}, "\n"),
			opts: scanner.ChunkOptions{
				TargetTokens: 6,
				MaxTokens:    8,
				MinLines:     1,
			},
			wantCount:    4,
			wantMinCount: 0,
			wantMax:      8,
		},
		{
			name:     "oversized block splits into max-sized windows",
			language: "go",
			source: strings.Join([]string{
				"package main",
				"",
				"func big() {",
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"cccccccccccccccccccccccccccccccc",
				"dddddddddddddddddddddddddddddddd",
				"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
				"ffffffffffffffffffffffffffffffff",
				"}",
			}, "\n"),
			opts: scanner.ChunkOptions{
				TargetTokens: 10,
				MaxTokens:    12,
				MinLines:     1,
			},
			wantCount:    0,
			wantMinCount: 2,
			wantMax:      12,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chunks := scanner.ChunkSource("example.go", tt.language, tt.source, tt.opts)
			if tt.wantCount > 0 && len(chunks) != tt.wantCount {
				t.Fatalf("chunk count mismatch: got %d want %d", len(chunks), tt.wantCount)
			}

			if tt.wantMinCount > 0 && len(chunks) < tt.wantMinCount {
				t.Fatalf("chunk count too small: got %d want >= %d", len(chunks), tt.wantMinCount)
			}

			for i, chunk := range chunks {
				if tt.wantMax > 0 && chunk.TokenEstimate > tt.wantMax {
					t.Fatalf("chunk %d token estimate too large: got %d want <= %d", i, chunk.TokenEstimate, tt.wantMax)
				}

				if chunk.StartLine < 1 {
					t.Fatalf("chunk %d invalid start line: %d", i, chunk.StartLine)
				}

				if chunk.EndLine < chunk.StartLine {
					t.Fatalf("chunk %d invalid end line: %d", i, chunk.EndLine)
				}

				if strings.TrimSpace(chunk.Content) == "" {
					t.Fatalf("chunk %d should not be empty", i)
				}
			}
		})
	}
}
