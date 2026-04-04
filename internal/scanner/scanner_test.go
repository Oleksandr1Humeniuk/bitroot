package scanner_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"bitroot/internal/scanner"
)

func TestScannerScanSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name:  "empty directory",
			files: map[string]string{},
		},
		{
			name: "scans nested files",
			files: map[string]string{
				"file1.txt":          "content1",
				"subdir/file2.go":    "package main",
				"subdir/deep/a.json": `{"ok":true}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rootDir := t.TempDir()
			for relPath, content := range tt.files {
				fullPath := filepath.Join(rootDir, relPath)
				if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
					t.Fatalf("mkdir failed: %v", err)
				}

				if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
					t.Fatalf("write file failed: %v", err)
				}
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			s := scanner.NewScanner(2, logger)

			results, err := s.Scan(context.Background(), rootDir)
			if err != nil {
				t.Fatalf("Scan returned error: %v", err)
			}

			var gotPaths []string
			gotSizes := make(map[string]int64, len(tt.files))
			for result := range results {
				if result.Error != nil {
					t.Fatalf("unexpected file scan error for %s: %v", result.Path, result.Error)
				}

				gotPaths = append(gotPaths, result.Path)
				gotSizes[result.Path] = result.Size
			}

			wantPaths := make([]string, 0, len(tt.files))
			for relPath := range tt.files {
				wantPaths = append(wantPaths, filepath.Join(rootDir, relPath))
			}

			sort.Strings(gotPaths)
			sort.Strings(wantPaths)
			if len(gotPaths) != len(wantPaths) {
				t.Fatalf("got %d files, want %d", len(gotPaths), len(wantPaths))
			}

			for i := range gotPaths {
				if gotPaths[i] != wantPaths[i] {
					t.Fatalf("path mismatch at %d: got %q want %q", i, gotPaths[i], wantPaths[i])
				}
			}

			for relPath, content := range tt.files {
				path := filepath.Join(rootDir, relPath)
				if gotSizes[path] != int64(len(content)) {
					t.Fatalf("size mismatch for %s: got %d want %d", path, gotSizes[path], len(content))
				}
			}
		})
	}
}

func TestScannerScanValidationErrors(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := scanner.NewScanner(1, logger)

	filePath := filepath.Join(t.TempDir(), "single.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	tests := []struct {
		name    string
		ctx     context.Context
		rootDir string
	}{
		{name: "nil context", ctx: nil, rootDir: t.TempDir()},
		{name: "empty path", ctx: context.Background(), rootDir: ""},
		{name: "missing path", ctx: context.Background(), rootDir: filepath.Join(t.TempDir(), "missing")},
		{name: "non directory path", ctx: context.Background(), rootDir: filePath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Scan(tt.ctx, tt.rootDir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestScannerScanRespectsCancelledContext(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := scanner.NewScanner(1, logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := s.Scan(ctx, rootDir)
	if err != nil {
		t.Fatalf("Scan returned unexpected error: %v", err)
	}

	for result := range results {
		t.Fatalf("expected no results when context is cancelled, got: %+v", result)
	}
}

func TestScannerScanCancelsMidTraversal(t *testing.T) {
	rootDir := t.TempDir()

	totalFiles := 2000
	for i := 0; i < totalFiles; i++ {
		subDir := filepath.Join(rootDir, "dir", strings.Repeat("x", i%5))
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}

		filePath := filepath.Join(subDir, fmt.Sprintf("file-%03d-%s.txt", i, strings.Repeat("a", i%7)))
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := scanner.NewScanner(4, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, err := s.Scan(ctx, rootDir)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	seen := 0
	for result := range results {
		if result.Error != nil {
			t.Fatalf("unexpected file scan error for %s: %v", result.Path, result.Error)
		}

		seen++
		if seen == 10 {
			cancel()
		}
	}

	if seen < 10 {
		t.Fatalf("expected at least 10 results before cancellation, got %d", seen)
	}

	if seen >= totalFiles {
		t.Fatalf("expected cancellation to stop early, got all %d files", seen)
	}
}
