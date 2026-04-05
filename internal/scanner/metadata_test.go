package scanner_test

import (
	"strings"
	"testing"

	"bitroot/internal/scanner"
)

func TestExtractFileContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		language        string
		source          string
		wantPackage     string
		wantHeaderMatch string
		wantImportMin   int
	}{
		{
			name:     "go metadata extraction",
			language: "go",
			source: strings.Join([]string{
				"// Package scanner handles indexing",
				"// for BitRoot",
				"package scanner",
				"",
				"import (",
				"\t\"context\"",
				"\t\"strings\"",
				")",
				"",
				"func Run() {}",
			}, "\n"),
			wantPackage:     "scanner",
			wantHeaderMatch: "Package scanner handles indexing",
			wantImportMin:   2,
		},
		{
			name:     "typescript extracts imports and symbol package",
			language: "typescript",
			source: strings.Join([]string{
				"// TS module",
				"import { readFile } from 'fs'",
				"import { readFile as readFileAgain } from 'fs'",
				"import path from 'path'",
				"export function buildIndex() {}",
			}, "\n"),
			wantPackage:     "buildIndex",
			wantHeaderMatch: "TS module",
			wantImportMin:   2,
		},
		{
			name:     "python extracts imports and declaration package",
			language: "python",
			source: strings.Join([]string{
				"\"\"\"Module doc",
				"with additional detail",
				"\"\"\"",
				"import os, sys",
				"from pathlib import Path",
				"def analyze():",
				"    return True",
			}, "\n"),
			wantPackage:     "analyze",
			wantHeaderMatch: "Module doc",
			wantImportMin:   3,
		},
		{
			name:     "markdown extracts title package",
			language: "markdown",
			source: strings.Join([]string{
				"# Project Notes",
				"",
				"Some content here",
			}, "\n"),
			wantPackage:     "Project Notes",
			wantHeaderMatch: "# Project Notes",
			wantImportMin:   0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meta := scanner.ExtractFileContext(tt.language, tt.source)
			if meta.PackageName != tt.wantPackage {
				t.Fatalf("package mismatch: got %q want %q", meta.PackageName, tt.wantPackage)
			}
			if tt.wantHeaderMatch != "" && !strings.Contains(meta.Header, tt.wantHeaderMatch) {
				t.Fatalf("header mismatch: got %q missing %q", meta.Header, tt.wantHeaderMatch)
			}
			if len(meta.PrimaryImports) < tt.wantImportMin {
				t.Fatalf("imports too small: got %d want >= %d", len(meta.PrimaryImports), tt.wantImportMin)
			}
		})
	}
}
