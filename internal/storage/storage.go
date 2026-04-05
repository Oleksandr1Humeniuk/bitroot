package storage

import (
	"encoding/json"
	"os"
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
	ProjectRoot string               `json:"project_root"`
	Files       map[string]FileEntry `json:"files"`
}

func (p *ProjectIndex) Save(path string) error {
	if p.Files == nil {
		p.Files = make(map[string]FileEntry)
	}

	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, body, 0o644)
}

func (p *ProjectIndex) Load(path string) error {
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

	return nil
}
