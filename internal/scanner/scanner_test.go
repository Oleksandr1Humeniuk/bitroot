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
		name          string
		files         map[string]string
		wantLanguages map[string]string
	}{
		{
			name:          "empty directory",
			files:         map[string]string{},
			wantLanguages: map[string]string{},
		},
		{
			name: "scans nested files",
			files: map[string]string{
				"file1.go":         "package main",
				"subdir/file2.ts":  "const x = 1;",
				"subdir/deep/a.md": "# title",
			},
			wantLanguages: map[string]string{
				"file1.go":         "go",
				"subdir/file2.ts":  "typescript",
				"subdir/deep/a.md": "markdown",
			},
		},
		{
			name: "filters ignored directories and extensions",
			files: map[string]string{
				"src/main.go":                  "package main",
				"src/ignore.bin":               "binary",
				"src/ignore.map":               "sourcemap",
				"src/ignore.json":              "{}",
				"src/.hidden.ts":               "const hidden = true",
				"node_modules/pkg/index.js":    "module.exports = {}",
				"vendor/lib/lib.go":            "package lib",
				"dist/build.js":                "console.log('x')",
				"build/out.js":                 "console.log('x')",
				"coverage/lcov.info":           "TN:",
				".next/server/chunks/main.js":  "module.exports = {}",
				".vercel/output/config.json":   "{}",
				".git/hooks/pre-commit.sample": "#!/bin/sh",
				"src/component/index.tsx":      "export const X = () => null",
				"src/component/index.jsx":      "export default function X() { return null }",
			},
			wantLanguages: map[string]string{
				"src/main.go":             "go",
				"src/component/index.tsx": "tsx",
				"src/component/index.jsx": "jsx",
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
			gotLanguages := make(map[string]string, len(tt.files))
			for result := range results {
				if result.Error != nil {
					t.Fatalf("unexpected file scan error for %s: %v", result.Path, result.Error)
				}

				gotPaths = append(gotPaths, result.Path)
				gotSizes[result.Path] = result.Size
				gotLanguages[result.Path] = result.Language
			}

			wantPaths := make([]string, 0, len(tt.wantLanguages))
			for relPath := range tt.wantLanguages {
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
				if _, ok := tt.wantLanguages[relPath]; !ok {
					continue
				}

				path := filepath.Join(rootDir, relPath)
				if gotSizes[path] != int64(len(content)) {
					t.Fatalf("size mismatch for %s: got %d want %d", path, gotSizes[path], len(content))
				}
			}

			for relPath, language := range tt.wantLanguages {
				path := filepath.Join(rootDir, relPath)
				if gotLanguages[path] != language {
					t.Fatalf("language mismatch for %s: got %q want %q", path, gotLanguages[path], language)
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
		wantErr bool
	}{
		{name: "nil context", ctx: nil, rootDir: t.TempDir(), wantErr: true},
		{name: "empty path", ctx: context.Background(), rootDir: "", wantErr: true},
		{name: "missing path", ctx: context.Background(), rootDir: filepath.Join(t.TempDir(), "missing"), wantErr: true},
		{name: "single file path", ctx: context.Background(), rootDir: filePath, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := s.Scan(tt.ctx, tt.rootDir)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			count := 0
			for range results {
				count++
			}

			if count != 0 {
				t.Fatalf("expected 0 results for unsupported single file, got %d", count)
			}
		})
	}
}

func TestScannerScanSingleAllowedFile(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	filePath := filepath.Join(rootDir, "main.go")
	if err := os.WriteFile(filePath, []byte("package main"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := scanner.NewScanner(1, logger)

	results, err := s.Scan(context.Background(), filePath)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	count := 0
	for result := range results {
		count++
		if result.Path != filePath {
			t.Fatalf("unexpected file path: %s", result.Path)
		}
		if result.Language != "go" {
			t.Fatalf("expected language go, got %s", result.Language)
		}
	}

	if count != 1 {
		t.Fatalf("expected 1 result, got %d", count)
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

		filePath := filepath.Join(subDir, fmt.Sprintf("file-%03d-%s.go", i, strings.Repeat("a", i%7)))
		if err := os.WriteFile(filePath, []byte("package main"), 0o644); err != nil {
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
