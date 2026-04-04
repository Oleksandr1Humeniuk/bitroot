package main

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"bitroot/internal/ai"
	"bitroot/internal/scanner"

	"github.com/joho/godotenv"
)

func main() {
	path := flag.String("path", ".", "Directory path to scan")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := godotenv.Load(); err != nil {
		logger.Warn("failed to load .env", "error", err)
	}

	aiClient, err := ai.NewClient(
		os.Getenv("AI_BASE_URL"),
		os.Getenv("AI_API_KEY"),
		os.Getenv("AI_MODEL"),
	)
	if err != nil {
		logger.Error("failed to initialize ai client", "error", err)
		os.Exit(1)
	}

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

			code, err := readFilePrefix(metadata.Path, 4000)
			if err != nil {
				failedFiles++
				logger.Warn("failed to read file", "path", metadata.Path, "error", err)
				continue
			}

			summary, err := aiClient.AnalyzeCode(ctx, string(code))
			if err != nil {
				failedFiles++
				logger.Warn("ai analysis failed", "path", metadata.Path, "error", err)
				continue
			}

			logger.Info("ai analysis", "file", metadata.Path, "summary", summary)
		}
	}
}

func readFilePrefix(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(io.LimitReader(file, maxBytes))
}
