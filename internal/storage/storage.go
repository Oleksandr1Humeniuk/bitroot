package storage

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileEntry struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	Language  string    `json:"language"`
	Summary   string    `json:"summary"`
	Package   string    `json:"package,omitempty"`
	Imports   []string  `json:"imports,omitempty"`
	Header    string    `json:"header,omitempty"`
	Refs      []string  `json:"refs,omitempty"`
	Chunks    []Chunk   `json:"chunks,omitempty"`
	Embedding []float64 `json:"embedding,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Chunk struct {
	Ref     string `json:"ref"`
	Content string `json:"content,omitempty"`
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
	Package  string
	Imports  []string
	Header   string
	Refs     []string
	MatchRef string
	Match    string
	Score    float64
}

var tokenSplitPattern = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
var identifierWordPattern = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)

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
			Package:  entry.Package,
			Imports:  append([]string(nil), entry.Imports...),
			Header:   entry.Header,
			Refs:     append([]string(nil), entry.Refs...),
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

func (p *ProjectIndex) Search(query []float64, limit int) ([]SearchResult, error) {
	return p.SearchSimilar(query, limit, "")
}

func (p *ProjectIndex) HybridSearch(query string, queryEmbedding []float64, limit int) ([]SearchResult, error) {
	return p.HybridSearchWithThreshold(query, queryEmbedding, limit, 0)
}

func (p *ProjectIndex) HybridSearchWithThreshold(query string, queryEmbedding []float64, limit int, minScore float64) ([]SearchResult, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if len(queryEmbedding) == 0 {
		return nil, errors.New("query embedding is required")
	}
	if limit < 1 {
		limit = 5
	}
	if minScore < 0 {
		minScore = 0
	}

	dimension := p.vectorDimensionNoLock()
	if dimension > 0 && len(queryEmbedding) != dimension {
		return nil, errors.New("query embedding dimension mismatch")
	}

	tokens := splitQueryTokens(query)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	boostTokens := extractBoostTokens(query)
	results := make([]SearchResult, 0, len(p.Files)*2)
	for _, entry := range p.Files {
		if len(entry.Embedding) == 0 || len(entry.Embedding) != len(queryEmbedding) {
			continue
		}

		semanticScore, ok := CosineSimilarity(queryEmbedding, entry.Embedding)
		if !ok {
			continue
		}

		chunks := entry.Chunks
		if len(chunks) == 0 {
			chunks = []Chunk{{Ref: entry.Path, Content: entry.Summary}}
		}

		for _, chunk := range chunks {
			chunkContent := strings.TrimSpace(chunk.Content)
			if chunkContent == "" {
				continue
			}

			keywordScore := keywordMatchScore(tokens, chunk.Ref, chunkContent)
			score := semanticScore + (0.2 * keywordScore)

			if lowerQuery != "" && strings.Contains(strings.ToLower(chunkContent), lowerQuery) {
				score += 1.0
			}
			if hasExactLongTokenMatch(boostTokens, chunkContent) {
				score += 1.0
			}

			if exactSymbolMatch(query, chunk.Ref, chunkContent) {
				score += 0.5
			}

			if score < minScore {
				continue
			}

			snippet := queryFocusedSnippet(chunkContent, lowerQuery, boostTokens, 1400)
			results = append(results, SearchResult{
				Path:     entry.Path,
				Summary:  entry.Summary,
				Language: entry.Language,
				Package:  entry.Package,
				Imports:  append([]string(nil), entry.Imports...),
				Header:   entry.Header,
				Refs:     append([]string(nil), entry.Refs...),
				MatchRef: strings.TrimSpace(chunk.Ref),
				Match:    snippet,
				Score:    score,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		iHigh := results[i].Score > 1.0
		jHigh := results[j].Score > 1.0
		if iHigh != jHigh {
			return iHigh
		}
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

func splitQueryTokens(query string) []string {
	parts := tokenSplitPattern.Split(strings.ToLower(strings.TrimSpace(query)), -1)
	if len(parts) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || len(part) < 2 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}

	return out
}

func extractBoostTokens(query string) []string {
	matches := identifierWordPattern.FindAllString(query, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		token := strings.ToLower(strings.TrimSpace(match))
		if len(token) <= 6 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}

	return out
}

func hasExactLongTokenMatch(boostTokens []string, content string) bool {
	if len(boostTokens) == 0 || strings.TrimSpace(content) == "" {
		return false
	}

	contentTokens := identifierWordPattern.FindAllString(strings.ToLower(content), -1)
	if len(contentTokens) == 0 {
		return false
	}

	wordSet := make(map[string]struct{}, len(contentTokens))
	for _, token := range contentTokens {
		wordSet[token] = struct{}{}
	}

	for _, token := range boostTokens {
		if _, ok := wordSet[token]; ok {
			return true
		}
	}

	return false
}

func keywordMatchScore(tokens []string, path, summary string) float64 {
	if len(tokens) == 0 {
		return 0
	}

	haystack := strings.ToLower(path + " " + summary)
	matches := 0
	for _, token := range tokens {
		if strings.Contains(haystack, token) {
			matches++
		}
	}

	if matches == 0 {
		return 0
	}

	return float64(matches) / float64(len(tokens))
}

func exactSymbolMatch(query, path, summary string) bool {
	needle := strings.TrimSpace(strings.ToLower(query))
	if needle == "" {
		return false
	}

	haystack := strings.ToLower(path + "\n" + summary)
	if strings.Contains(haystack, needle) {
		return true
	}

	queryTokens := identifierTokens(needle)
	if len(queryTokens) == 0 {
		return false
	}

	haystackTokens := identifierTokens(haystack)
	if len(haystackTokens) == 0 {
		return false
	}

	for token := range queryTokens {
		if _, ok := haystackTokens[token]; !ok {
			return false
		}
	}

	return true
}

func compactSnippet(content string, max int) string {
	if max < 1 {
		max = 1
	}
	content = strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if len(content) <= max {
		return content
	}
	return strings.TrimSpace(content[:max]) + "..."
}

func queryFocusedSnippet(content, lowerQuery string, boostTokens []string, max int) string {
	if max < 1 {
		max = 1
	}

	normalized := strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n"))
	if normalized == "" {
		return ""
	}

	lowerContent := strings.ToLower(normalized)
	focusIdx := -1
	focusLen := 0

	if lowerQuery != "" {
		if idx := strings.Index(lowerContent, lowerQuery); idx >= 0 {
			focusIdx = idx
			focusLen = len(lowerQuery)
		}
	}

	if focusIdx == -1 {
		for _, token := range boostTokens {
			if token == "" {
				continue
			}
			if idx := strings.Index(lowerContent, token); idx >= 0 {
				focusIdx = idx
				focusLen = len(token)
				break
			}
		}
	}

	if focusIdx == -1 || len(normalized) <= max {
		return compactSnippet(normalized, max)
	}

	start := focusIdx - (max / 3)
	if start < 0 {
		start = 0
	}
	end := start + max
	if end > len(normalized) {
		end = len(normalized)
		start = end - max
		if start < 0 {
			start = 0
		}
	}

	if focusLen > 0 && focusIdx+focusLen > end {
		end = focusIdx + focusLen
		if end > len(normalized) {
			end = len(normalized)
		}
	}

	out := strings.TrimSpace(normalized[start:end])
	if start > 0 {
		out = "...\n" + out
	}
	if end < len(normalized) {
		out = out + "\n..."
	}

	return out
}

func identifierTokens(value string) map[string]struct{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	parts := tokenSplitPattern.Split(value, -1)
	out := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		for _, token := range splitCamelToken(part) {
			if token == "" {
				continue
			}
			out[token] = struct{}{}
		}
	}

	return out
}

func splitCamelToken(s string) []string {
	if s == "" {
		return nil
	}

	var parts []string
	start := 0
	for i := 1; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			continue
		}
		if s[i-1] >= 'a' && s[i-1] <= 'z' && s[i] >= 'A' && s[i] <= 'Z' {
			parts = append(parts, strings.ToLower(s[start:i]))
			start = i
		}
	}
	parts = append(parts, strings.ToLower(s[start:]))

	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			clean = append(clean, part)
		}
	}

	return clean
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
