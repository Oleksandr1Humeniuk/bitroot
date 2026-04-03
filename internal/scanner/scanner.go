package scanner

import (
	"context"
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
	Error    error // To capture errors during file processing
}

// Scanner orchestrates the file scanning process using a worker pool.
type Scanner struct {
	WorkerCount int
	Logger      *slog.Logger
}

// NewScanner creates a new Scanner instance.
func NewScanner(workerCount int, logger *slog.Logger) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{
		WorkerCount: workerCount,
		Logger:      logger,
	}
}

// Scan initiates the file scanning process for a given root directory.
// It uses a worker pool to process files concurrently and returns a channel
// for collecting FileMetadata, or an error if the scanning cannot be started.
func (s *Scanner) Scan(ctx context.Context, rootDir string) (<-chan FileMetadata, error) {
	jobs := make(chan string)
	results := make(chan FileMetadata)

	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < s.WorkerCount; i++ {
		wg.Add(1)
		go s.worker(ctx, i+1, jobs, results, &wg)
	}

	// Walk the file system and send jobs
	go func() {
		defer close(jobs)
		err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				s.Logger.Error("filepath.WalkDir error", "path", path, "error", err)
				// Continue walking even if there's an error with one file/directory
				return nil
			}
			if !d.IsDir() {
				select {
				case jobs <- path:
				case <-ctx.Done():
					return ctx.Err() // Stop walking if context is cancelled
				}
			}
			return nil
		})
		if err != nil && err != context.Canceled {
			s.Logger.Error("Error walking directory", "rootDir", rootDir, "error", err)
		}
	}()

	// Wait for all workers to finish and then close the results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	return results, nil
}

// worker is a goroutine that processes file scanning jobs.
func (s *Scanner) worker(ctx context.Context, id int, jobs <-chan string, results chan<- FileMetadata, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case job, ok := <-jobs:
			if !ok {
				s.Logger.Debug("Worker received close signal on jobs channel", "workerID", id)
				return // Jobs channel closed, no more jobs
			}
			s.Logger.Debug("Worker processing file", "workerID", id, "file", job)

			metadata := FileMetadata{
				Path:     job,
				FileName: filepath.Base(job),
			}

			f, err := os.Open(job)
			if err != nil {
				metadata.Error = err
				s.Logger.Error("Error opening file", "workerID", id, "file", job, "error", err)
			} else {
				defer f.Close()
				info, err := f.Stat()
				if err != nil {
					metadata.Error = err
					s.Logger.Error("Error statting file after opening", "workerID", id, "file", job, "error", err)
				} else {
					metadata.Size = info.Size()
				}
			}

			// Send the metadata (with or without error) to the results channel
			select {
			case results <- metadata:
			case <-ctx.Done():
				s.Logger.Warn("Worker context cancelled while sending result", "workerID", id, "file", job)
				return // Context cancelled, stop processing
			}
		case <-ctx.Done():
			s.Logger.Warn("Worker context cancelled while waiting for job", "workerID", id)
			return // Context cancelled, stop processing
		}
	}
}
