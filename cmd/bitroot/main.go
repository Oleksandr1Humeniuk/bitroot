package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bitroot/internal/scanner"
)

func main() {
	dirPath := flag.String("dir", ".", "Directory path to scan")
	workerCount := flag.Int("workers", 4, "Number of concurrent workers")
	flag.Parse()

	if *dirPath == "" {
		slog.Error("Error: --dir flag is required")
		os.Exit(1)
	}

	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Setup context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("Received shutdown signal, initiating graceful termination...")
		cancel() // Signal cancellation to all goroutines
	}()

	slog.Info("Starting BitRoot scanner", "directory", *dirPath, "workers", *workerCount)

	s := scanner.NewScanner(*workerCount, logger)
	fileMetadataChan, err := s.Scan(ctx, *dirPath)
	if err != nil {
		slog.Error("Failed to start scanner", "error", err)
		os.Exit(1)
	}

	var totalFiles int
	// Collect results from the scanner
	for {
		select {
		case metadata, ok := <-fileMetadataChan:
			if !ok {
				slog.Info("Scanner finished all jobs.")
				goto endProgram // Exit the loop when the channel is closed
			}
			totalFiles++
			// Process file metadata (e.g., print, store, send to AI for analysis)
			if metadata.Error != nil {
				slog.Warn("Error processing file", "path", metadata.Path, "error", metadata.Error)
			} else {
				slog.Info("Scanned file", "path", metadata.Path, "size", metadata.Size)
			}
		case <-ctx.Done():
			slog.Info("Main program context cancelled. Shutting down...")
			goto endProgram // Exit the loop on context cancellation
		}
	}

endProgram:
	slog.Info("BitRoot program terminated.", "totalFilesScanned", totalFiles)
	// Give a moment for goroutines to clean up
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}
