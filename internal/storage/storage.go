package storage

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"sort"
	"sync"
	"time"
)

type FileEntry struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	Language  string    `json:"language"`
	Summary   string    `json:"summary"`
	Embedding []float64 `json:"embedding,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ProjectIndex struct {
	mu              sync.RWMutex         `json:"-"`
	ProjectRoot     string               `json:"project_root"`
	VectorDimension int                  `json:"vector_dimension,omitempty"`
	Files           map[string]FileEntry `json:"files"`
}

type SearchResult struct {
	Path     string
	Summary  string
	Language string
	Score    float64
}

func (p *ProjectIndex) Save(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.Files == nil {
		p.Files = make(map[string]FileEntry)
	}
	p.ensureVectorDimension()

	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, body, 0o644)
}

func (p *ProjectIndex) Load(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if len(body) == 0 {
		p.Files = make(map[string]FileEntry)
		return nil
	}

	if err := json.Unmarshal(body, p); err != nil {
		return err
	}

	if p.Files == nil {
		p.Files = make(map[string]FileEntry)
	}
	p.ensureVectorDimension()

	return nil
}

func (p *ProjectIndex) Upsert(entry FileEntry) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry.Path == "" {
		return errors.New("entry path is required")
	}

	if p.Files == nil {
		p.Files = make(map[string]FileEntry)
	}

	if len(entry.Embedding) > 0 {
		if p.VectorDimension == 0 {
			p.VectorDimension = len(entry.Embedding)
		}

		if len(entry.Embedding) != p.VectorDimension {
			return errors.New("embedding dimension mismatch")
		}
	}

	p.Files[entry.Path] = entry

	return nil
}

func (p *ProjectIndex) Get(path string) (FileEntry, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	entry, ok := p.Files[path]
	return entry, ok
}

func (p *ProjectIndex) SearchSimilar(query []float64, limit int, excludePath string) ([]SearchResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(query) == 0 {
		return nil, errors.New("query embedding is required")
	}

	if limit < 1 {
		limit = 5
	}

	dimension := p.vectorDimensionNoLock()
	if dimension > 0 && len(query) != dimension {
		return nil, errors.New("query embedding dimension mismatch")
	}

	results := make([]SearchResult, 0, len(p.Files))
	for path, entry := range p.Files {
		if path == excludePath {
			continue
		}
		if len(entry.Embedding) == 0 {
			continue
		}
		if len(entry.Embedding) != len(query) {
			continue
		}

		score, ok := CosineSimilarity(query, entry.Embedding)
		if !ok {
			continue
		}

		results = append(results, SearchResult{
			Path:     entry.Path,
			Summary:  entry.Summary,
			Language: entry.Language,
			Score:    score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Path < results[j].Path
		}
		return results[i].Score > results[j].Score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func (p *ProjectIndex) ensureVectorDimension() {
	if p.VectorDimension > 0 {
		return
	}
	p.VectorDimension = p.inferVectorDimensionNoLock()
}

func (p *ProjectIndex) vectorDimensionNoLock() int {
	if p.VectorDimension > 0 {
		return p.VectorDimension
	}

	return p.inferVectorDimensionNoLock()
}

func (p *ProjectIndex) inferVectorDimensionNoLock() int {

	for _, entry := range p.Files {
		if len(entry.Embedding) > 0 {
			return len(entry.Embedding)
		}
	}

	return 0
}

// CosineSimilarity computes cosine similarity between two vectors.
// It returns ok=false for invalid or zero-norm vectors.
func CosineSimilarity(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}

	dot := 0.0
	normA := 0.0
	normB := 0.0

	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0, false
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), true
}
