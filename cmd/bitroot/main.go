package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"bitroot/internal/scanner"
)

func main() {
	path := flag.String("path", ".", "Directory path to scan")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rootDir := *path
	workerCount := 4
	logger.Info("starting scanner", "path", rootDir, "workers", workerCount)

	s := scanner.NewScanner(workerCount, logger)
	results, err := s.Scan(ctx, rootDir)
	if err != nil {
		logger.Error("failed to start scanner", "error", err)
		os.Exit(1)
	}

	var scannedFiles int
	var failedFiles int

	for {
		select {
		case <-ctx.Done():
			logger.Info("scan interrupted", "error", ctx.Err(), "scanned_files", scannedFiles, "failed_files", failedFiles)
			return
		case metadata, ok := <-results:
			if !ok {
				logger.Info("scan completed", "scanned_files", scannedFiles, "failed_files", failedFiles)
				return
			}

			if metadata.Error != nil {
				failedFiles++
				logger.Warn("scan error", "path", metadata.Path, "error", metadata.Error)
				continue
			}

			scannedFiles++
			logger.Info("file", "name", metadata.FileName, "size", metadata.Size)
		}
	}
}
