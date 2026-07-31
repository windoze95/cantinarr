// Package mediapath validates and translates per-instance arr media paths.
//
// Arr paths belong to the remote service's namespace and may use POSIX,
// Windows drive, or Windows UNC syntax independently of the operating system
// running Cantinarr. Cantinarr paths and allowed roots, by contrast, always
// use the local operating system's native path syntax.
package mediapath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mapping translates an absolute path prefix reported by an arr service to a
// directory visible to Cantinarr. The Cantinarr path must be contained by one
// of the server operator's allowed media roots.
type Mapping struct {
	ArrPath       string `json:"arr_path"`
	CantinarrPath string `json:"cantinarr_path"`
}

type sourceKind uint8

const (
	sourcePOSIX sourceKind = iota + 1
	sourceWindowsDrive
	sourceWindowsUNC
)

type sourcePath struct {
	kind       sourceKind
	drive      string
	uncServer  string
	uncShare   string
	components []string
}

type allowedRoot struct {
	lexical  string
	resolved string
}

// Validate checks mappings against the local allowed roots and returns a
// normalized copy. It does not mutate the caller's slice.
//
// Source paths are normalized according to their own syntax, not the local
// operating system. Local targets must exist, be directories, be lexically
// contained by an allowed root, and remain contained after symlink resolution.
func Validate(mappings []Mapping, allowedRoots []string) ([]Mapping, error) {
	roots, err := validateAllowedRoots(allowedRoots)
	if err != nil {
		return nil, err
	}

	result := make([]Mapping, 0, len(mappings))
	seenSources := make(map[string]struct{}, len(mappings))
	for index, mapping := range mappings {
		source, err := parseSourcePath(mapping.ArrPath)
		if err != nil {
			return nil, fmt.Errorf("mapping %d arr_path: %w", index+1, err)
		}
		sourceKey := source.key()
		if _, exists := seenSources[sourceKey]; exists {
			return nil, fmt.Errorf("mapping %d arr_path duplicates another mapping", index+1)
		}

		target, resolvedTarget, err := validateNativeDirectory(mapping.CantinarrPath)
		if err != nil {
			return nil, fmt.Errorf("mapping %d cantinarr_path: %w", index+1, err)
		}
		if !containedByAllowedRoot(target, resolvedTarget, roots) {
			return nil, fmt.Errorf("mapping %d cantinarr_path is outside the configured media roots", index+1)
		}

		seenSources[sourceKey] = struct{}{}
		result = append(result, Mapping{
			ArrPath:       source.normalized(),
			CantinarrPath: target,
		})
	}
	return result, nil
}

// Translate maps one live arr-reported file path into Cantinarr's native
// filesystem namespace. The most specific component-bound prefix wins.
// Mappings should normally be the normalized result of Validate; malformed or
// ambiguous input fails closed.
func Translate(reportedPath string, mappings []Mapping) (string, bool) {
	reported, err := parseSourcePath(reportedPath)
	if err != nil {
		return "", false
	}

	bestIndex := -1
	bestDepth := -1
	var bestSource sourcePath
	seenAtBestDepth := false
	for index, mapping := range mappings {
		source, err := parseSourcePath(mapping.ArrPath)
		if err != nil || !source.isPrefixOf(reported) {
			continue
		}
		if _, err := normalizeNativePath(mapping.CantinarrPath); err != nil {
			continue
		}

		depth := source.specificity()
		switch {
		case depth > bestDepth:
			bestIndex = index
			bestDepth = depth
			bestSource = source
			seenAtBestDepth = false
		case depth == bestDepth:
			// Two equally-specific prefixes that both match the same path are
			// ambiguous. Validate rejects duplicate normalized source prefixes,
			// but Translate also fails closed when handed unvalidated data.
			seenAtBestDepth = true
		}
	}
	if bestIndex < 0 || seenAtBestDepth {
		return "", false
	}

	target, err := normalizeNativePath(mappings[bestIndex].CantinarrPath)
	if err != nil {
		return "", false
	}
	suffix := reported.components[len(bestSource.components):]
	for _, component := range suffix {
		if !safeNativeComponent(component) {
			return "", false
		}
	}
	if len(suffix) == 0 {
		return target, true
	}
	parts := make([]string, 0, len(suffix)+1)
	parts = append(parts, target)
	parts = append(parts, suffix...)
	translated := filepath.Join(parts...)
	if !pathWithin(target, translated) {
		return "", false
	}
	return translated, true
}

func validateAllowedRoots(values []string) ([]allowedRoot, error) {
	roots := make([]allowedRoot, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		lexical, resolved, err := validateNativeDirectory(value)
		if err != nil {
			return nil, fmt.Errorf("allowed root %d: %w", index+1, err)
		}
		if filepath.Dir(lexical) == lexical || filepath.Dir(resolved) == resolved {
			return nil, fmt.Errorf("allowed root %d is too broad", index+1)
		}
		key := filepath.Clean(lexical)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, allowedRoot{lexical: lexical, resolved: resolved})
	}
	return roots, nil
}

func validateNativeDirectory(value string) (string, string, error) {
	cleaned, err := normalizeNativePath(value)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", "", fmt.Errorf("is not accessible: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("must be a directory")
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", "", fmt.Errorf("cannot resolve symlinks: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", "", fmt.Errorf("cannot normalize resolved path: %w", err)
	}
	return cleaned, filepath.Clean(resolved), nil
}

func normalizeNativePath(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("is required")
	}
	if err := validateText(value); err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("must be an absolute native path")
	}
	if hasNativeTraversal(value) {
		return "", fmt.Errorf("must not contain traversal components")
	}
	return filepath.Clean(value), nil
}

func containedByAllowedRoot(target, resolvedTarget string, roots []allowedRoot) bool {
	for _, root := range roots {
		if pathWithin(root.lexical, target) && pathWithin(root.resolved, resolvedTarget) {
			return true
		}
	}
	return false
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func hasNativeTraversal(value string) bool {
	for _, component := range strings.FieldsFunc(value, func(r rune) bool {
		if r == filepath.Separator {
			return true
		}
		// Windows accepts slash as a separator even though its native
		// separator is backslash.
		return filepath.Separator == '\\' && r == '/'
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func parseSourcePath(value string) (sourcePath, error) {
	if value == "" {
		return sourcePath{}, fmt.Errorf("is required")
	}
	if err := validateText(value); err != nil {
		return sourcePath{}, err
	}

	switch {
	case isWindowsUNC(value):
		return parseWindowsUNC(value)
	case len(value) >= 2 && value[1] == ':':
		return parseWindowsDrive(value)
	case strings.HasPrefix(value, "/"):
		return parsePOSIX(value)
	default:
		return sourcePath{}, fmt.Errorf("must be an absolute POSIX, Windows drive, or UNC path")
	}
}

func parsePOSIX(value string) (sourcePath, error) {
	components, err := splitSourceComponents(value, func(r rune) bool { return r == '/' }, false)
	if err != nil {
		return sourcePath{}, err
	}
	return sourcePath{kind: sourcePOSIX, components: components}, nil
}

func parseWindowsDrive(value string) (sourcePath, error) {
	if len(value) < 3 || !asciiLetter(value[0]) || (value[2] != '\\' && value[2] != '/') {
		return sourcePath{}, fmt.Errorf("must be an absolute Windows drive path")
	}
	components, err := splitSourceComponents(value[3:], isWindowsSeparator, true)
	if err != nil {
		return sourcePath{}, err
	}
	return sourcePath{
		kind:       sourceWindowsDrive,
		drive:      strings.ToUpper(value[:2]),
		components: components,
	}, nil
}

func parseWindowsUNC(value string) (sourcePath, error) {
	trimmed := strings.TrimLeftFunc(value, isWindowsSeparator)
	components, err := splitSourceComponents(trimmed, isWindowsSeparator, true)
	if err != nil {
		return sourcePath{}, err
	}
	if len(components) < 2 {
		return sourcePath{}, fmt.Errorf("UNC path must include a server and share")
	}
	return sourcePath{
		kind:       sourceWindowsUNC,
		uncServer:  components[0],
		uncShare:   components[1],
		components: components[2:],
	}, nil
}

func splitSourceComponents(value string, separator func(rune) bool, windows bool) ([]string, error) {
	raw := strings.FieldsFunc(value, separator)
	components := make([]string, 0, len(raw))
	for _, component := range raw {
		switch component {
		case ".", "":
			continue
		case "..":
			return nil, fmt.Errorf("must not contain traversal components")
		}
		if windows && strings.ContainsRune(component, ':') {
			return nil, fmt.Errorf("contains an invalid Windows path component")
		}
		components = append(components, component)
	}
	return components, nil
}

func isWindowsUNC(value string) bool {
	if strings.HasPrefix(value, `\\`) {
		return true
	}
	// A leading pair of forward slashes is the portable spelling of a UNC
	// path. Three or more leading slashes remain POSIX-style input.
	return strings.HasPrefix(value, "//") && !strings.HasPrefix(value, "///")
}

func isWindowsSeparator(r rune) bool {
	return r == '\\' || r == '/'
}

func asciiLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func validateText(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("must be valid UTF-8")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	return nil
}

func safeNativeComponent(component string) bool {
	if component == "" || component == "." || component == ".." {
		return false
	}
	if strings.ContainsRune(component, filepath.Separator) {
		return false
	}
	return filepath.Separator != '\\' || !strings.ContainsRune(component, '/')
}

func (p sourcePath) normalized() string {
	switch p.kind {
	case sourcePOSIX:
		if len(p.components) == 0 {
			return "/"
		}
		return "/" + strings.Join(p.components, "/")
	case sourceWindowsDrive:
		if len(p.components) == 0 {
			return p.drive + `\`
		}
		return p.drive + `\` + strings.Join(p.components, `\`)
	case sourceWindowsUNC:
		base := `\\` + p.uncServer + `\` + p.uncShare
		if len(p.components) == 0 {
			return base
		}
		return base + `\` + strings.Join(p.components, `\`)
	default:
		return ""
	}
}

func (p sourcePath) key() string {
	normalized := p.normalized()
	if p.kind == sourcePOSIX {
		return "posix:" + normalized
	}
	return "windows:" + strings.ToLower(normalized)
}

func (p sourcePath) specificity() int {
	return len(p.components)
}

func (p sourcePath) isPrefixOf(other sourcePath) bool {
	if p.kind != other.kind || len(p.components) > len(other.components) {
		return false
	}
	switch p.kind {
	case sourceWindowsDrive:
		if !strings.EqualFold(p.drive, other.drive) {
			return false
		}
	case sourceWindowsUNC:
		if !strings.EqualFold(p.uncServer, other.uncServer) || !strings.EqualFold(p.uncShare, other.uncShare) {
			return false
		}
	}
	for index, component := range p.components {
		if p.kind == sourcePOSIX {
			if component != other.components[index] {
				return false
			}
			continue
		}
		if !strings.EqualFold(component, other.components[index]) {
			return false
		}
	}
	return true
}
