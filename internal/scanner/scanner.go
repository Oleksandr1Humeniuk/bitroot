package scanner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// FileMetadata represents the metadata collected for a scanned file.
type FileMetadata struct {
	Path     string
	FileName string
	Size     int64
	Error    error
}

// FileScanner defines the scanner contract.
type FileScanner interface {
	Scan(ctx context.Context, rootDir string) (<-chan FileMetadata, error)
}

// Scanner orchestrates the file scanning process.
type Scanner struct {
	WorkerCount int
	Logger      *slog.Logger
}

// NewScanner creates a new Scanner instance.
func NewScanner(workerCount int, logger *slog.Logger) *Scanner {
	if workerCount < 1 {
		workerCount = 1
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Scanner{
		WorkerCount: workerCount,
		Logger:      logger,
	}
}

// Scan recursively discovers files under rootDir.
func (s *Scanner) Scan(ctx context.Context, rootDir string) (<-chan FileMetadata, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}

	if rootDir == "" {
		return nil, errors.New("root directory is required")
	}

	rootInfo, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("stat root directory %q: %w", rootDir, err)
	}

	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("root path %q is not a directory", rootDir)
	}

	results := make(chan FileMetadata, 100)
	jobs := make(chan string, 100)
	var wg sync.WaitGroup

	for i := 0; i < s.WorkerCount; i++ {
		wg.Add(1)
		go s.worker(ctx, jobs, results, &wg)
	}

	go func() {
		defer close(jobs)

		err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				s.Logger.Error("walk entry failed", "path", path, "error", walkErr)
				return nil
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				return nil
			}

			select {
			case jobs <- path:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})

		if err != nil && !errors.Is(err, context.Canceled) {
			s.Logger.Error("walk failed", "root_dir", rootDir, "error", err)
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	return results, nil
}

func (s *Scanner) worker(ctx context.Context, jobs <-chan string, results chan<- FileMetadata, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-jobs:
			if !ok {
				return
			}

			metadata := FileMetadata{
				Path:     path,
				FileName: filepath.Base(path),
			}

			info, err := os.Stat(path)
			if err != nil {
				metadata.Error = fmt.Errorf("stat file %q: %w", path, err)
				s.Logger.Warn("file stat failed", "path", path, "error", err)
			} else {
				metadata.Size = info.Size()
				s.Logger.Info("found file", "path", path, "size", metadata.Size)
			}

			select {
			case results <- metadata:
			case <-ctx.Done():
				return
			}
		}
	}
}
