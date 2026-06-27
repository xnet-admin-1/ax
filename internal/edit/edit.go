package edit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Apply performs a SEARCH/REPLACE edit on a file.
// Returns the new content and any error.
func Apply(path, search, replace string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		// If file doesn't exist and search is empty, create it
		if os.IsNotExist(err) && search == "" {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			return os.WriteFile(path, []byte(replace), 0644)
		}
		return err
	}

	original := string(content)

	// Empty search = append to file
	if search == "" {
		return os.WriteFile(path, []byte(original+replace), 0644)
	}

	result, ok := tryReplace(original, search, replace)
	if !ok {
		return fmt.Errorf("SEARCH block not found in %s\n\nSearched for:\n%s", path, search)
	}

	return os.WriteFile(path, []byte(result), 0644)
}

// tryReplace attempts exact match, then whitespace-flexible match.
func tryReplace(content, search, replace string) (string, bool) {
	// 1. Exact match
	if strings.Contains(content, search) {
		return strings.Replace(content, search, replace, 1), true
	}

	// 2. Whitespace-flexible: normalize leading whitespace
	result, ok := flexibleReplace(content, search, replace)
	if ok {
		return result, true
	}

	// 3. Trimmed lines match (handles trailing whitespace differences)
	result, ok = trimmedReplace(content, search, replace)
	if ok {
		return result, true
	}

	return "", false
}

// flexibleReplace matches ignoring indentation level differences.
// If search has consistent indent offset from content, adjusts replacement accordingly.
func flexibleReplace(content, search, replace string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	searchLines := strings.Split(search, "\n")

	if len(searchLines) == 0 {
		return "", false
	}

	// Find the search block allowing indent differences
	for i := 0; i <= len(contentLines)-len(searchLines); i++ {
		offset, match := matchWithIndentOffset(contentLines[i:i+len(searchLines)], searchLines)
		if match {
			// Apply same indent offset to replacement
			replaceLines := strings.Split(replace, "\n")
			adjusted := applyIndentOffset(replaceLines, offset)

			// Rebuild content
			result := strings.Join(contentLines[:i], "\n")
			if i > 0 {
				result += "\n"
			}
			result += strings.Join(adjusted, "\n")
			if i+len(searchLines) < len(contentLines) {
				result += "\n" + strings.Join(contentLines[i+len(searchLines):], "\n")
			}
			return result, true
		}
	}
	return "", false
}

// matchWithIndentOffset checks if lines match with a consistent indent offset.
func matchWithIndentOffset(content, search []string) (int, bool) {
	if len(content) != len(search) {
		return 0, false
	}

	offset := -999
	for i := range content {
		cTrimmed := strings.TrimLeft(content[i], " \t")
		sTrimmed := strings.TrimLeft(search[i], " \t")

		// Both empty lines match regardless
		if cTrimmed == "" && sTrimmed == "" {
			continue
		}

		// Content after indent must match
		if cTrimmed != sTrimmed {
			return 0, false
		}

		cIndent := len(content[i]) - len(cTrimmed)
		sIndent := len(search[i]) - len(sTrimmed)
		lineOffset := cIndent - sIndent

		if offset == -999 {
			offset = lineOffset
		} else if offset != lineOffset {
			return 0, false
		}
	}

	if offset == -999 {
		offset = 0
	}
	return offset, true
}

// applyIndentOffset adds/removes spaces from each line.
func applyIndentOffset(lines []string, offset int) []string {
	if offset == 0 {
		return lines
	}
	result := make([]string, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			result[i] = line
			continue
		}
		if offset > 0 {
			result[i] = strings.Repeat(" ", offset) + line
		} else {
			trim := -offset
			if trim > len(line)-len(strings.TrimLeft(line, " ")) {
				trim = len(line) - len(strings.TrimLeft(line, " "))
			}
			result[i] = line[trim:]
		}
	}
	return result
}

// trimmedReplace matches after trimming trailing whitespace from each line.
func trimmedReplace(content, search, replace string) (string, bool) {
	contentLines := strings.Split(content, "\n")
	searchLines := strings.Split(search, "\n")

	for i := 0; i <= len(contentLines)-len(searchLines); i++ {
		match := true
		for j := range searchLines {
			if strings.TrimRight(contentLines[i+j], " \t") != strings.TrimRight(searchLines[j], " \t") {
				match = false
				break
			}
		}
		if match {
			result := strings.Join(contentLines[:i], "\n")
			if i > 0 {
				result += "\n"
			}
			result += replace
			if i+len(searchLines) < len(contentLines) {
				result += "\n" + strings.Join(contentLines[i+len(searchLines):], "\n")
			}
			return result, true
		}
	}
	return "", false
}
