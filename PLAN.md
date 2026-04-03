# Project Plan: BitRoot

## Phase 0: Environment & Architecture (Current Phase)

### 1. Environment Setup (Completed)
- Created `.gitignore` for Go projects (including binaries, OS-specific files, .env).
- Created `.env.example` for future LLM API keys.
- Created `README.md` explaining BitRoot's purpose.

### 2. Concurrent File Scanner Architecture Plan

**Goal:** Implement a high-performance, concurrent file scanner using a worker pool pattern, supporting graceful termination and testability.

**Approach:** Worker Pool Pattern

The scanner will utilize a fixed-size worker pool. File scanning tasks (jobs) will be sent to a job channel, and results (file metadata) will be collected from a result channel. `context.Context` will be used to manage graceful termination of workers and the main process.

**Structure Outline:**

#### `internal/scanner/scanner.go`

```go
package scanner

import (
	"context"
	"path/filepath"
)

// FileMetadata represents the metadata collected for a scanned file.
type FileMetadata struct {
	Path     string
	FileName string
	Size     int64
	// Add other relevant metadata fields here (e.g., hash, last modified time)
}

// Scanner orchestrates the file scanning process using a worker pool.
type Scanner struct {
	WorkerCount int
	// Add other configuration fields as needed
}

// NewScanner creates a new Scanner instance.
func NewScanner(workerCount int) *Scanner {
	return &Scanner{
		WorkerCount: workerCount,
	}
}

// Scan initiates the file scanning process for a given root directory.
// It uses a worker pool to process files concurrently and returns a channel
// for collecting FileMetadata, or an error if the scanning cannot be started.
func (s *Scanner) Scan(ctx context.Context, rootDir string) (<-chan FileMetadata, error) {
	// TODO: Implementation for job and result channels, worker dispatch, and directory traversal
	return nil, nil // Placeholder
}

// worker is a goroutine that processes file scanning jobs.
func worker(ctx context.Context, id int, jobs <-chan string, results chan<- FileMetadata) {
	// TODO: Implementation for file reading, metadata extraction, and error handling
	// Each worker will listen on the 'jobs' channel, perform scanning, and send results to the 'results' channel.
	// It should also respect the 'ctx.Done()' signal for graceful shutdown.
}
```

#### `cmd/bitroot/main.go`

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bitroot/internal/scanner" // Assuming 'bitroot' is the module name
)

func main() {
	dirPath := flag.String("dir", ".", "Directory path to scan")
	workerCount := flag.Int("workers", 4, "Number of concurrent workers")
	flag.Parse()

	if *dirPath == "" {
		log.Fatalf("Error: --dir flag is required")
	}

	// Setup context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("
Received shutdown signal, initiating graceful termination...")
		cancel() // Signal cancellation to all goroutines
	}()

	fmt.Printf("Starting BitRoot scanner in directory: %s with %d workers
", *dirPath, *workerCount)

	s := scanner.NewScanner(*workerCount)
	fileMetadataChan, err := s.Scan(ctx, *dirPath)
	if err != nil {
		log.Fatalf("Failed to start scanner: %v", err)
	}

	// Collect results from the scanner
	for {
		select {
		case metadata, ok := <-fileMetadataChan:
			if !ok {
				fmt.Println("Scanner finished all jobs.")
				goto endProgram // Exit the loop when the channel is closed
			}
			// Process file metadata (e.g., print, store, send to AI for analysis)
			fmt.Printf("Scanned file: %s (Size: %d bytes)
", metadata.Path, metadata.Size)
		case <-ctx.Done():
			fmt.Println("Main program context cancelled. Shutting down...")
			goto endProgram // Exit the loop on context cancellation
		}
	}

endProgram:
	fmt.Println("BitRoot program terminated.")
	// Add any necessary cleanup here before exiting
	time.Sleep(500 * time.Millisecond) // Give a moment for goroutines to clean up
	os.Exit(0)
}
```

**Requirements Addressed:**
- **Worker Pool Pattern:** Explicitly outlined in `internal/scanner/scanner.go` (`worker` function, job/result channels will be added in implementation) and `Scanner` struct.
- **`context.Context` for graceful termination:** Utilized in `cmd/bitroot/main.go` for signal handling and passed to `s.Scan`. The `worker` function in `internal/scanner/scanner.go` will also receive and respect the context.
- **`internal/scanner` decoupled and testable:** The `scanner` package is self-contained with clear `Scanner` struct and `Scan` method, facilitating independent testing. No direct dependencies on `cmd/bitroot`.
- **No implementation code for the scanner yet:** The outlines contain `TODO` comments and placeholder return values, adhering to this requirement.
