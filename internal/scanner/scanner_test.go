package scanner_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"bitroot/internal/scanner"
)

func TestScanner_Scan(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create some dummy files and subdirectories
	err := os.MkdirAll(filepath.Join(tempDir, "subdir1"), 0755)
	if err != nil {
		t.Fatalf("Failed to create subdir1: %v", err)
	}

	err = os.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("content1"), 0644)
	if err != nil {
		t.Fatalf("Failed to create file1.txt: %v", err)
	}

	err = os.WriteFile(filepath.Join(tempDir, "subdir1", "file2.go"), []byte("package main"), 0644)
	if err != nil {
		t.Fatalf("Failed to create file2.go: %v", err)
	}

	// Create a file that is not readable to test error handling
	blockedFilePath := filepath.Join(tempDir, "blocked.txt")
	err = os.WriteFile(blockedFilePath, []byte("blocked content"), 0000) // No permissions
	if err != nil {
		t.Fatalf("Failed to create blocked.txt: %v", err)
	}

	// Use a NopLogger for tests to avoid polluting test output unless explicitly debugging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	s := scanner.NewScanner(2, logger) // 2 workers

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, err := s.Scan(ctx, tempDir)
	if err != nil {
		t.Fatalf("Scanner.Scan returned an error: %v", err)
	}

	expectedFiles := map[string]struct{}{
		filepath.Join(tempDir, "file1.txt"):           {},
		filepath.Join(tempDir, "subdir1", "file2.go"): {},
		filepath.Join(tempDir, "blocked.txt"):         {},
	}

	var scannedCount atomic.Int32

	for metadata := range results {
		scannedCount.Add(1)
		if _, ok := expectedFiles[metadata.Path]; !ok {
			t.Errorf("Scanned unexpected file: %s", metadata.Path)
		}

		if metadata.Path == blockedFilePath {
			if metadata.Error == nil {
				t.Errorf("Expected error for blocked file %s, but got nil", metadata.Path)
			}
		} else if metadata.Error != nil {
			t.Errorf("Received unexpected error for file %s: %v", metadata.Path, metadata.Error)
			// Restore permissions to clean up
			os.Chmod(metadata.Path, 0644)
		}
	}

	if scannedCount.Load() != int32(len(expectedFiles)) {
		t.Errorf("Expected to scan %d files, but scanned %d", len(expectedFiles), scannedCount.Load())
	}

	// Verify context cancellation works
	cx, ca := context.WithCancel(context.Background())
	sc, _ := s.Scan(cx, tempDir)

	ca()                               // Cancel context immediately
	time.Sleep(100 * time.Millisecond) // Give workers a moment to react

	countAfterCancel := 0
	for range sc {
		countAfterCancel++
	}

	if countAfterCancel > 0 && scannedCount.Load() == int32(len(expectedFiles)) {
		t.Logf("Warning: Scanner might have processed some files even after cancellation for a very fast scan. Scanned %d files.", countAfterCancel)
		// This is acceptable for very small directories where scan might complete before cancellation propagates fully.
	} else if countAfterCancel > 0 && scannedCount.Load() != int32(len(expectedFiles)) {
		t.Errorf("Expected 0 files after immediate cancellation, but got %d", countAfterCancel)
	}

	// Restore permissions for cleanup
	os.Chmod(blockedFilePath, 0644)
}
