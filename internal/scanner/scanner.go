package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileMetadata represents the metadata collected for a scanned file.
type FileMetadata struct {
	Path     string
	FileName string
	Size     int64
	Language string
	Hash     string
	Error    error
}

var ignoredDirectories = map[string]struct{}{
	".git":         {},
	".next":        {},
	".vercel":      {},
	"build":        {},
	"coverage":     {},
	"node_modules": {},
	"dist":         {},
	"vendor":       {},
}

var languageByExtension = map[string]string{
	".go":  "go",
	".ts":  "typescript",
	".js":  "javascript",
	".tsx": "tsx",
	".jsx": "jsx",
	".md":  "markdown",
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
		return nil, err
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

		if !rootInfo.IsDir() {
			if shouldProcessFile(rootDir) {
				select {
				case jobs <- rootDir:
				case <-ctx.Done():
				}
			}

			return
		}

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
				if shouldIgnoreDirectory(d.Name()) {
					return filepath.SkipDir
				}

				return nil
			}

			if !shouldProcessFile(path) {
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
				Language: detectLanguage(path),
			}

			hash, size, err := hashFile(path)
			if err != nil {
				metadata.Error = err
				s.Logger.Warn("file hash failed", "path", path, "error", err)
			} else {
				metadata.Size = size
				metadata.Hash = hash
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

func shouldIgnoreDirectory(name string) bool {
	_, ok := ignoredDirectories[strings.ToLower(name)]
	return ok
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	language, ok := languageByExtension[ext]
	if !ok {
		return ""
	}

	return language
}

func shouldProcessFile(path string) bool {
	name := filepath.Base(path)
	if strings.HasPrefix(name, ".") {
		return false
	}

	return detectLanguage(path) != ""
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hasher.Sum(nil)), info.Size(), nil
}
