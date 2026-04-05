package scanner

import (
	"regexp"
	"strings"
)

var (
	goPackagePattern      = regexp.MustCompile(`^\s*package\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	goImportSinglePattern = regexp.MustCompile(`^\s*import\s+"([^"]+)"`)
	goImportLinePattern   = regexp.MustCompile(`^\s*"([^"]+)"`)

	jsImportFromPattern = regexp.MustCompile(`^\s*import\s+.+?\s+from\s+['"]([^'"]+)['"]`)
	jsImportBarePattern = regexp.MustCompile(`^\s*import\s+['"]([^'"]+)['"]`)
	jsRequirePattern    = regexp.MustCompile(`^\s*(?:const|let|var)\s+[a-zA-Z_$][a-zA-Z0-9_$]*\s*=\s*require\(\s*['"]([^'"]+)['"]\s*\)`)
	jsExportNamePattern = regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:class|function)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	jsDeclNamePattern   = regexp.MustCompile(`^\s*(?:class|function)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

	pyImportPattern      = regexp.MustCompile(`^\s*import\s+(.+)$`)
	pyFromImportPattern  = regexp.MustCompile(`^\s*from\s+([a-zA-Z0-9_\.]+)\s+import\s+`)
	pyDeclNamePattern    = regexp.MustCompile(`^\s*(?:class|def)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	markdownTitlePattern = regexp.MustCompile(`^\s*#\s+(.+)$`)
)

type FileContextMetadata struct {
	PackageName    string
	PrimaryImports []string
	Header         string
}

func ExtractFileContext(language, source string) FileContextMetadata {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	if strings.TrimSpace(source) == "" {
		return FileContextMetadata{}
	}

	lines := strings.Split(source, "\n")
	meta := FileContextMetadata{Header: extractHeader(lines)}

	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go":
		meta.PackageName = extractGoPackage(lines)
		meta.PrimaryImports = extractGoImports(lines, 6)
	case "typescript", "javascript", "tsx", "jsx":
		meta.PackageName = extractJSPackage(lines)
		meta.PrimaryImports = extractJSImports(lines, 6)
	case "python":
		meta.PackageName = extractPythonPackage(lines)
		meta.PrimaryImports = extractPythonImports(lines, 6)
	case "markdown":
		meta.PackageName = extractMarkdownTitle(lines)
	}

	return meta
}

func extractGoPackage(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}

		match := goPackagePattern.FindStringSubmatch(line)
		if len(match) == 2 {
			return strings.TrimSpace(match[1])
		}

		return ""
	}

	return ""
}

func extractGoImports(lines []string, max int) []string {
	if max < 1 {
		max = 1
	}

	imports := make([]string, 0, max)
	seen := make(map[string]struct{}, max)
	inBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if inBlock {
			if trimmed == ")" {
				inBlock = false
				continue
			}

			match := goImportLinePattern.FindStringSubmatch(trimmed)
			if len(match) == 2 {
				value := strings.TrimSpace(match[1])
				if value != "" {
					if _, ok := seen[value]; !ok {
						seen[value] = struct{}{}
						imports = append(imports, value)
						if len(imports) >= max {
							return imports
						}
					}
				}
			}

			continue
		}

		if strings.HasPrefix(trimmed, "import (") {
			inBlock = true
			continue
		}

		match := goImportSinglePattern.FindStringSubmatch(trimmed)
		if len(match) == 2 {
			value := strings.TrimSpace(match[1])
			if value != "" {
				if _, ok := seen[value]; !ok {
					seen[value] = struct{}{}
					imports = append(imports, value)
					if len(imports) >= max {
						return imports
					}
				}
			}
		}
	}

	return imports
}

func extractHeader(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	const maxHeaderLines = 12
	collected := make([]string, 0, maxHeaderLines)
	inBlockComment := false
	inDocstring := false
	docstringDelim := ""
	started := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if !started {
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
				started = true
			} else {
				return ""
			}
		}

		if strings.HasPrefix(trimmed, "/*") {
			inBlockComment = true
		}
		docstringJustOpened := false
		if strings.HasPrefix(trimmed, `"""`) {
			inDocstring = true
			docstringDelim = `"""`
			docstringJustOpened = true
		}
		if strings.HasPrefix(trimmed, `'''`) {
			inDocstring = true
			docstringDelim = `'''`
			docstringJustOpened = true
		}

		if inBlockComment || inDocstring || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*") {
			if trimmed != "" {
				collected = append(collected, trimmed)
				if len(collected) >= maxHeaderLines {
					break
				}
			}
		} else {
			break
		}

		if inBlockComment && strings.Contains(trimmed, "*/") {
			inBlockComment = false
		}
		if inDocstring && docstringDelim != "" && docstringClosed(trimmed, docstringDelim, docstringJustOpened) {
			inDocstring = false
			docstringDelim = ""
		}

		if !inBlockComment && !inDocstring && trimmed == "" {
			break
		}
	}

	return strings.TrimSpace(strings.Join(collected, "\n"))
}

func extractJSImports(lines []string, max int) []string {
	if max < 1 {
		max = 1
	}

	imports := make([]string, 0, max)
	seen := make(map[string]struct{}, max)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}

		candidates := []*regexp.Regexp{jsImportFromPattern, jsImportBarePattern, jsRequirePattern}
		for _, pattern := range candidates {
			match := pattern.FindStringSubmatch(trimmed)
			if len(match) != 2 {
				continue
			}
			value := strings.TrimSpace(match[1])
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			imports = append(imports, value)
			if len(imports) >= max {
				return imports
			}
			break
		}
	}

	return imports
}

func extractJSPackage(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}

		if match := jsExportNamePattern.FindStringSubmatch(trimmed); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
		if match := jsDeclNamePattern.FindStringSubmatch(trimmed); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}

	return ""
}

func docstringClosed(line, delim string, openedOnSameLine bool) bool {
	count := strings.Count(line, delim)
	if count == 0 {
		return false
	}

	if openedOnSameLine {
		return count >= 2
	}

	return count >= 1
}

func extractPythonImports(lines []string, max int) []string {
	if max < 1 {
		max = 1
	}

	imports := make([]string, 0, max)
	seen := make(map[string]struct{}, max)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if match := pyFromImportPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			value := strings.TrimSpace(match[1])
			if value != "" {
				if _, ok := seen[value]; !ok {
					seen[value] = struct{}{}
					imports = append(imports, value)
					if len(imports) >= max {
						return imports
					}
				}
			}
			continue
		}

		if match := pyImportPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			segments := strings.Split(match[1], ",")
			for _, seg := range segments {
				value := strings.TrimSpace(seg)
				if idx := strings.Index(value, " as "); idx > 0 {
					value = strings.TrimSpace(value[:idx])
				}
				if value == "" {
					continue
				}
				if _, ok := seen[value]; ok {
					continue
				}
				seen[value] = struct{}{}
				imports = append(imports, value)
				if len(imports) >= max {
					return imports
				}
			}
		}
	}

	return imports
}

func extractPythonPackage(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
			continue
		}

		if match := pyDeclNamePattern.FindStringSubmatch(trimmed); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}

	return ""
}

func extractMarkdownTitle(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := markdownTitlePattern.FindStringSubmatch(trimmed); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
		return ""
	}

	return ""
}
