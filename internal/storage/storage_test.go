package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectIndexSaveLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		index     ProjectIndex
		writeData []byte
		wantErr   bool
		errCheck  func(error) bool
	}{
		{
			name: "save and load valid index",
			index: ProjectIndex{
				ProjectRoot: "/tmp/project",
				Files: map[string]FileEntry{
					"main.go": {
						Path:      "main.go",
						Hash:      "abc123",
						Language:  "go",
						Summary:   "Main entrypoint",
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

			if len(tt.index.Files) > 0 || tt.index.ProjectRoot != "" {
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

			if loaded.ProjectRoot != tt.index.ProjectRoot {
				t.Fatalf("project root mismatch: got %q want %q", loaded.ProjectRoot, tt.index.ProjectRoot)
			}

			if len(loaded.Files) != len(tt.index.Files) {
				t.Fatalf("file count mismatch: got %d want %d", len(loaded.Files), len(tt.index.Files))
			}

			for key, expected := range tt.index.Files {
				got, ok := loaded.Files[key]
				if !ok {
					t.Fatalf("missing key %q", key)
				}

				if got.Path != expected.Path || got.Hash != expected.Hash || got.Language != expected.Language || got.Summary != expected.Summary {
					t.Fatalf("entry mismatch for %q", key)
				}
			}
		})
	}
}
