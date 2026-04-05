package storage

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectIndexSaveLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		index     *ProjectIndex
		writeData []byte
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "save and load valid index",
			index: &ProjectIndex{
				ProjectRoot: "/tmp/project",
				Files: map[string]FileEntry{
					"main.go": {
						Path:      "main.go",
						Hash:      "abc123",
						Language:  "go",
						Summary:   "Main entrypoint",
						Package:   "main",
						Imports:   []string{"context", "fmt"},
						Header:    "// main entrypoint file",
						Chunks:    []Chunk{{Ref: "main.go:1-20", Content: "func main() { run() }"}},
						Refs:      []string{"main.go:1-20"},
						Embedding: []float64{0.25, 0.5, 0.75},
						UpdatedAt: time.Now().UTC().Truncate(time.Second),
					},
				},
			},
		},
		{
			name:      "load invalid json",
			writeData: []byte("{invalid json"),
			wantErr:   true,
		},
		{
			name:     "load missing file",
			wantErr:  true,
			errCheck: func(err error) bool { return errors.Is(err, os.ErrNotExist) },
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			indexPath := filepath.Join(tmpDir, "index.json")

			if tt.writeData != nil {
				if err := os.WriteFile(indexPath, tt.writeData, 0o644); err != nil {
					t.Fatalf("write test file failed: %v", err)
				}
			}

			if tt.index != nil && (len(tt.index.Files) > 0 || tt.index.ProjectRoot != "") {
				if err := tt.index.Save(indexPath); err != nil {
					t.Fatalf("save failed: %v", err)
				}
			}

			var loaded ProjectIndex
			err := loaded.Load(indexPath)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errCheck != nil && !tt.errCheck(err) {
					t.Fatalf("error did not match expected check: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("load failed: %v", err)
			}

			expected := tt.index
			if expected == nil {
				expected = &ProjectIndex{}
			}

			if loaded.ProjectRoot != expected.ProjectRoot {
				t.Fatalf("project root mismatch: got %q want %q", loaded.ProjectRoot, expected.ProjectRoot)
			}

			if loaded.VectorDimension != expected.VectorDimension && expected.VectorDimension != 0 {
				t.Fatalf("vector dimension mismatch: got %d want %d", loaded.VectorDimension, expected.VectorDimension)
			}

			if len(loaded.Files) != len(expected.Files) {
				t.Fatalf("file count mismatch: got %d want %d", len(loaded.Files), len(expected.Files))
			}

			for key, expected := range expected.Files {
				got, ok := loaded.Files[key]
				if !ok {
					t.Fatalf("missing key %q", key)
				}

				if got.Path != expected.Path || got.Hash != expected.Hash || got.Language != expected.Language || got.Summary != expected.Summary {
					t.Fatalf("entry mismatch for %q", key)
				}

				if got.Package != expected.Package || got.Header != expected.Header {
					t.Fatalf("metadata mismatch for %q", key)
				}
				if len(got.Imports) != len(expected.Imports) {
					t.Fatalf("imports length mismatch for %q: got %d want %d", key, len(got.Imports), len(expected.Imports))
				}
				for i := range expected.Imports {
					if got.Imports[i] != expected.Imports[i] {
						t.Fatalf("import mismatch for %q at index %d: got %q want %q", key, i, got.Imports[i], expected.Imports[i])
					}
				}
				if len(got.Chunks) != len(expected.Chunks) {
					t.Fatalf("chunks length mismatch for %q: got %d want %d", key, len(got.Chunks), len(expected.Chunks))
				}
				for i := range expected.Chunks {
					if got.Chunks[i].Ref != expected.Chunks[i].Ref || got.Chunks[i].Content != expected.Chunks[i].Content {
						t.Fatalf("chunk mismatch for %q at index %d", key, i)
					}
				}

				if len(got.Embedding) != len(expected.Embedding) {
					t.Fatalf("embedding length mismatch for %q: got %d want %d", key, len(got.Embedding), len(expected.Embedding))
				}

				if len(got.Refs) != len(expected.Refs) {
					t.Fatalf("refs length mismatch for %q: got %d want %d", key, len(got.Refs), len(expected.Refs))
				}
				for i := range expected.Refs {
					if got.Refs[i] != expected.Refs[i] {
						t.Fatalf("ref mismatch for %q at index %d: got %q want %q", key, i, got.Refs[i], expected.Refs[i])
					}
				}

				for i := range expected.Embedding {
					if got.Embedding[i] != expected.Embedding[i] {
						t.Fatalf("embedding mismatch for %q at index %d: got %f want %f", key, i, got.Embedding[i], expected.Embedding[i])
					}
				}
			}
		})
	}
}

func TestProjectIndexUpsertAndSearchSimilar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		entries        []FileEntry
		query          []float64
		excludePath    string
		limit          int
		wantErr        bool
		wantResultSize int
		wantFirstPath  string
	}{
		{
			name: "returns similarity-ranked results",
			entries: []FileEntry{
				{Path: "a.go", Summary: "a", Refs: []string{"a.go:1-9"}, Embedding: []float64{1, 0}},
				{Path: "b.go", Summary: "b", Embedding: []float64{0.8, 0.2}},
				{Path: "c.go", Summary: "c", Embedding: []float64{0, 1}},
			},
			query:          []float64{1, 0},
			limit:          2,
			wantResultSize: 2,
			wantFirstPath:  "a.go",
		},
		{
			name: "excludes provided path",
			entries: []FileEntry{
				{Path: "a.go", Summary: "a", Embedding: []float64{1, 0}},
				{Path: "b.go", Summary: "b", Embedding: []float64{0.9, 0.1}},
			},
			query:          []float64{1, 0},
			excludePath:    "a.go",
			limit:          2,
			wantResultSize: 1,
			wantFirstPath:  "b.go",
		},
		{
			name: "dimension mismatch returns error",
			entries: []FileEntry{
				{Path: "a.go", Summary: "a", Embedding: []float64{1, 0, 0}},
			},
			query:   []float64{1, 0},
			limit:   1,
			wantErr: true,
		},
		{
			name: "empty query returns error",
			entries: []FileEntry{
				{Path: "a.go", Summary: "a", Embedding: []float64{1, 0}},
			},
			query:   nil,
			limit:   1,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			index := ProjectIndex{ProjectRoot: "/tmp/project", Files: make(map[string]FileEntry)}
			for _, entry := range tt.entries {
				if err := index.Upsert(entry); err != nil {
					t.Fatalf("upsert failed: %v", err)
				}
			}

			results, err := index.SearchSimilar(tt.query, tt.limit, tt.excludePath)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("search failed: %v", err)
			}

			if len(results) != tt.wantResultSize {
				t.Fatalf("result size mismatch: got %d want %d", len(results), tt.wantResultSize)
			}

			if tt.wantResultSize > 0 && results[0].Path != tt.wantFirstPath {
				t.Fatalf("first result mismatch: got %q want %q", results[0].Path, tt.wantFirstPath)
			}

			if tt.wantResultSize > 0 && tt.wantFirstPath == "a.go" && len(results[0].Refs) == 0 {
				t.Fatal("expected refs in first result")
			}

			for i := range results {
				if math.IsNaN(results[i].Score) || math.IsInf(results[i].Score, 0) {
					t.Fatalf("invalid similarity score at %d: %f", i, results[i].Score)
				}
			}
		})
	}
}

func TestProjectIndexSaveWritesEmbeddingsAlongsideSummary(t *testing.T) {
	t.Parallel()

	index := &ProjectIndex{
		ProjectRoot: "/tmp/project",
		Files: map[string]FileEntry{
			"src/main.go": {
				Path:      "src/main.go",
				Hash:      "hash1",
				Language:  "go",
				Summary:   "Main application entry point.",
				Embedding: []float64{0.11, 0.22, 0.33},
				UpdatedAt: time.Now().UTC().Truncate(time.Second),
			},
		},
	}

	vectorStorePath := filepath.Join(t.TempDir(), ".bitroot_vector_store.json")
	if err := index.Save(vectorStorePath); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	body, err := os.ReadFile(vectorStorePath)
	if err != nil {
		t.Fatalf("read vector store failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal vector store failed: %v", err)
	}

	files, ok := payload["files"].(map[string]any)
	if !ok {
		t.Fatalf("files section missing or invalid: %#v", payload["files"])
	}

	entryRaw, ok := files["src/main.go"].(map[string]any)
	if !ok {
		t.Fatalf("entry missing for src/main.go")
	}

	summary, ok := entryRaw["summary"].(string)
	if !ok || summary == "" {
		t.Fatalf("summary missing or invalid: %#v", entryRaw["summary"])
	}

	embedding, ok := entryRaw["embedding"].([]any)
	if !ok {
		t.Fatalf("embedding missing or invalid: %#v", entryRaw["embedding"])
	}

	if len(embedding) != 3 {
		t.Fatalf("embedding length mismatch: got %d want 3", len(embedding))
	}
}

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		a       []float64
		b       []float64
		wantOK  bool
		wantMin float64
		wantMax float64
	}{
		{
			name:    "identical vectors",
			a:       []float64{1, 2, 3},
			b:       []float64{1, 2, 3},
			wantOK:  true,
			wantMin: 0.999999,
			wantMax: 1.000001,
		},
		{
			name:    "orthogonal vectors",
			a:       []float64{1, 0},
			b:       []float64{0, 1},
			wantOK:  true,
			wantMin: -0.000001,
			wantMax: 0.000001,
		},
		{
			name:   "dimension mismatch",
			a:      []float64{1, 2},
			b:      []float64{1, 2, 3},
			wantOK: false,
		},
		{
			name:   "zero norm vector",
			a:      []float64{0, 0},
			b:      []float64{1, 1},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := CosineSimilarity(tt.a, tt.b)
			if ok != tt.wantOK {
				t.Fatalf("ok mismatch: got %t want %t", ok, tt.wantOK)
			}

			if !tt.wantOK {
				return
			}

			if got < tt.wantMin || got > tt.wantMax {
				t.Fatalf("similarity out of bounds: got %f want between %f and %f", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestProjectIndexGet(t *testing.T) {
	t.Parallel()

	index := &ProjectIndex{Files: make(map[string]FileEntry)}
	entry := FileEntry{Path: "src/main.go", Summary: "entry"}
	if err := index.Upsert(entry); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	got, ok := index.Get("src/main.go")
	if !ok {
		t.Fatal("expected entry to exist")
	}
	if got.Path != entry.Path || got.Summary != entry.Summary {
		t.Fatalf("entry mismatch: got %+v want %+v", got, entry)
	}

	if _, ok := index.Get("missing.go"); ok {
		t.Fatal("expected missing entry to be absent")
	}
}

func TestProjectIndexSearch(t *testing.T) {
	t.Parallel()

	index := &ProjectIndex{Files: make(map[string]FileEntry)}
	if err := index.Upsert(FileEntry{Path: "a.go", Summary: "a", Embedding: []float64{1, 0}}); err != nil {
		t.Fatalf("upsert a failed: %v", err)
	}
	if err := index.Upsert(FileEntry{Path: "b.go", Summary: "b", Embedding: []float64{0, 1}}); err != nil {
		t.Fatalf("upsert b failed: %v", err)
	}

	results, err := index.Search([]float64{1, 0}, 1)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result size mismatch: got %d want 1", len(results))
	}
	if results[0].Path != "a.go" {
		t.Fatalf("unexpected top result: got %q want %q", results[0].Path, "a.go")
	}
}

func TestProjectIndexHybridSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		query          string
		queryEmbedding []float64
		entries        []FileEntry
		limit          int
		wantErr        bool
		wantTopPath    string
	}{
		{
			name:           "exact symbol boosts keyword hit",
			query:          "SearchHybrid",
			queryEmbedding: []float64{1, 0},
			entries: []FileEntry{
				{Path: "internal/ai/client.go", Summary: "GenerateAnswer handles qa output", Package: "ai", Imports: []string{"context"}, Embedding: []float64{0.7, 0.3}},
				{Path: "internal/storage/storage.go", Summary: "Search logic", Chunks: []Chunk{{Ref: "internal/storage/storage.go:180-240", Content: "func SearchHybrid(query string) {}"}}, Embedding: []float64{0.95, 0.05}},
			},
			limit:       2,
			wantTopPath: "internal/storage/storage.go",
		},
		{
			name:           "natural language query applies long token boost",
			query:          "How is tokenSplitPattern defined?",
			queryEmbedding: []float64{1, 0},
			entries: []FileEntry{
				{Path: "internal/storage/storage.go", Summary: "Token splitting helpers", Chunks: []Chunk{{Ref: "internal/storage/storage.go:54-70", Content: "var tokenSplitPattern = regexp.MustCompile(`[^a-zA-Z0-9_]+`)"}}, Embedding: []float64{0.95, 0.05}},
				{Path: "internal/ai/client.go", Summary: "AI client", Chunks: []Chunk{{Ref: "internal/ai/client.go:1-20", Content: "type Client struct{}"}}, Embedding: []float64{0.8, 0.2}},
			},
			limit:       3,
			wantTopPath: "internal/storage/storage.go",
		},
		{
			name:           "empty query fails",
			query:          "",
			queryEmbedding: []float64{1, 0},
			entries:        []FileEntry{{Path: "a.go", Summary: "a", Embedding: []float64{1, 0}}},
			limit:          1,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			index := &ProjectIndex{Files: make(map[string]FileEntry)}
			for _, entry := range tt.entries {
				if err := index.Upsert(entry); err != nil {
					t.Fatalf("upsert failed: %v", err)
				}
			}

			results, err := index.HybridSearch(tt.query, tt.queryEmbedding, tt.limit)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("hybrid search failed: %v", err)
			}
			if len(results) == 0 {
				t.Fatal("expected at least one result")
			}
			if tt.wantTopPath != "" && results[0].Path != tt.wantTopPath {
				t.Fatalf("top result mismatch: got %q want %q", results[0].Path, tt.wantTopPath)
			}
			if tt.wantTopPath == "internal/storage/storage.go" {
				if results[0].Score <= 1.3 {
					t.Fatalf("expected boosted score > 1.3, got %f", results[0].Score)
				}
				if results[0].MatchRef == "" || results[0].Match == "" {
					t.Fatalf("expected matched chunk data, got ref=%q match=%q", results[0].MatchRef, results[0].Match)
				}
			}
		})
	}
}

func TestProjectIndexHybridSearchWithThreshold(t *testing.T) {
	t.Parallel()

	index := &ProjectIndex{Files: make(map[string]FileEntry)}
	entries := []FileEntry{
		{Path: "high.go", Summary: "high relevance", Embedding: []float64{1, 0}},
		{Path: "low.go", Summary: "low relevance", Embedding: []float64{0.2, 0.8}},
	}
	for _, entry := range entries {
		if err := index.Upsert(entry); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}
	}

	results, err := index.HybridSearchWithThreshold("high", []float64{1, 0}, 5, 0.15)
	if err != nil {
		t.Fatalf("hybrid search with threshold failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one high-confidence result")
	}
	if results[0].Path != "high.go" {
		t.Fatalf("unexpected top result: got %q want high.go", results[0].Path)
	}

	strictResults, err := index.HybridSearchWithThreshold("high", []float64{1, 0}, 5, 3.0)
	if err != nil {
		t.Fatalf("hybrid search with strict threshold failed: %v", err)
	}
	if len(strictResults) != 0 {
		t.Fatalf("expected zero results with strict threshold, got %d", len(strictResults))
	}
}
