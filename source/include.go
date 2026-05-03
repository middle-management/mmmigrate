package source

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"
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
// Include paths are resolved relative to the root of fsys.
func ProcessIncludes(content string, fsys fs.FS) (string, []IncludeInfo, error) {
	return processIncludesRecursive(content, fsys, make(map[string]bool), nil)
}

func processIncludesRecursive(content string, fsys fs.FS, visiting map[string]bool, infos []IncludeInfo) (string, []IncludeInfo, error) {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))

	for i, line := range lines {
		match := includeRegex.FindStringSubmatch(line)
		if match == nil {
			result = append(result, line)
			continue
		}

		includePath := strings.TrimSpace(match[1])

		// Normalize and validate: fs.FS uses forward-slash paths and rejects
		// absolute / .. components. path.Clean drops a leading "./" but keeps
		// "../" so we explicitly reject anything that escapes root.
		cleanPath := path.Clean(includePath)
		if !fs.ValidPath(cleanPath) {
			return "", nil, fmt.Errorf("include path %s escapes base directory at line %d", includePath, i+1)
		}

		if visiting[cleanPath] {
			return "", nil, fmt.Errorf("circular include detected: %s at line %d", includePath, i+1)
		}

		includeContent, err := fs.ReadFile(fsys, cleanPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to read include file %s at line %d: %w", includePath, i+1, err)
		}

		checksum := fmt.Sprintf("%x", sha256.Sum256(includeContent))

		visiting[cleanPath] = true

		processedContent, nestedInfos, err := processIncludesRecursive(string(includeContent), fsys, visiting, infos)
		if err != nil {
			return "", nil, err
		}

		delete(visiting, cleanPath)

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
