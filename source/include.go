package source

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IncludeInfo tracks information about an included file.
type IncludeInfo struct {
	Path      string
	Checksum  string
	StartLine int
	EndLine   int
}

var includeRegex = regexp.MustCompile(`^\s*--\s*@include\s+(.+?)\s*$`)

// ProcessIncludes recursively expands @include directives in SQL content.
func ProcessIncludes(content string, baseDir string) (string, []IncludeInfo, error) {
	return processIncludesRecursive(content, baseDir, make(map[string]bool), nil)
}

func processIncludesRecursive(content string, baseDir string, visiting map[string]bool, infos []IncludeInfo) (string, []IncludeInfo, error) {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))

	for i, line := range lines {
		match := includeRegex.FindStringSubmatch(line)
		if match == nil {
			result = append(result, line)
			continue
		}

		includePath := strings.TrimSpace(match[1])

		fullPath := filepath.Join(baseDir, includePath)
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to resolve include path %s at line %d: %w", includePath, i+1, err)
		}

		if visiting[absPath] {
			return "", nil, fmt.Errorf("circular include detected: %s at line %d", includePath, i+1)
		}

		includeContent, err := os.ReadFile(absPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to read include file %s at line %d: %w", includePath, i+1, err)
		}

		checksum := fmt.Sprintf("%x", sha256.Sum256(includeContent))

		visiting[absPath] = true

		processedContent, nestedInfos, err := processIncludesRecursive(string(includeContent), baseDir, visiting, infos)
		if err != nil {
			return "", nil, err
		}

		delete(visiting, absPath)

		startLine := len(result) + 1
		includeLines := strings.Split(processedContent, "\n")
		endLine := startLine + len(includeLines) + 1

		info := IncludeInfo{
			Path:      includePath,
			Checksum:  checksum[:8],
			StartLine: startLine,
			EndLine:   endLine,
		}

		result = append(result, fmt.Sprintf("-- BEGIN INCLUDE: %s [checksum: %s]", includePath, info.Checksum))
		result = append(result, includeLines...)
		result = append(result, fmt.Sprintf("-- END INCLUDE: %s", includePath))

		infos = append(nestedInfos, info)
	}

	return strings.Join(result, "\n"), infos, nil
}
