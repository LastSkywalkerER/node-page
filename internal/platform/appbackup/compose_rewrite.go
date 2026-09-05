package appbackup

import (
	"fmt"
	"os"
	"strings"
)

// This file edits the operator's own compose file, so it does so surgically.
//
// Round-tripping through a YAML library would reformat the document and drop
// every comment — unacceptable for a file a human maintains. Instead the
// rewriter walks lines, tracks which service block it is inside, and replaces
// only the scalar after `image:`, preserving indentation, quote style and any
// trailing comment. Anything it does not positively recognise it leaves alone
// and reports as "service not found" rather than guessing.

// ComposeImages returns the image reference declared for each service in a
// compose document, keyed by service name. Services built from source (no
// `image:`) are absent.
func ComposeImages(doc string) map[string]string {
	out := map[string]string{}
	forEachServiceImage(doc, func(service, image string, _ int) {
		out[service] = image
	})
	return out
}

// RewriteImages replaces the image reference of the named services in doc.
// It returns the new document and the services it could not find, so the
// caller can fail loudly instead of silently updating nothing.
func RewriteImages(doc string, want map[string]string) (string, []string) {
	lines := strings.Split(doc, "\n")
	found := map[string]bool{}

	forEachServiceImage(doc, func(service, _ string, idx int) {
		target, ok := want[service]
		if !ok {
			return
		}
		lines[idx] = replaceImageValue(lines[idx], target)
		found[service] = true
	})

	var missing []string
	for svc := range want {
		if !found[svc] {
			missing = append(missing, svc)
		}
	}
	return strings.Join(lines, "\n"), missing
}

// forEachServiceImage invokes fn(service, image, lineIndex) for every
// `image:` declaration inside the top-level `services:` mapping.
func forEachServiceImage(doc string, fn func(service, image string, idx int)) {
	lines := strings.Split(doc, "\n")

	inServices := false
	servicesIndent := -1
	serviceIndent := -1
	current := ""

	for i, raw := range lines {
		if isBlankOrComment(raw) {
			continue
		}
		indent := leadingSpaces(raw)
		trimmed := strings.TrimSpace(raw)

		// Leaving the services mapping: any key at or left of its indent.
		if inServices && indent <= servicesIndent {
			inServices = false
			serviceIndent = -1
			current = ""
		}

		if !inServices {
			if key, _, ok := splitKey(trimmed); ok && key == "services" {
				inServices = true
				servicesIndent = indent
			}
			continue
		}

		// First key inside services fixes the service indentation level.
		if serviceIndent == -1 {
			serviceIndent = indent
		}

		if indent == serviceIndent {
			if key, _, ok := splitKey(trimmed); ok {
				current = key
			} else {
				current = ""
			}
			continue
		}

		// A key deeper than the service level belongs to the current service.
		if current != "" && indent > serviceIndent {
			if key, value, ok := splitKey(trimmed); ok && key == "image" {
				fn(current, unquote(stripComment(value)), i)
			}
		}
	}
}

// replaceImageValue swaps the scalar on an `image:` line, keeping the original
// indentation, key spacing, quote style and trailing comment.
func replaceImageValue(line, target string) string {
	idx := strings.Index(line, "image:")
	if idx < 0 {
		return line
	}
	head := line[:idx+len("image:")]
	rest := line[idx+len("image:"):]

	// Preserve the gap between the colon and the value.
	gap := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
	if gap == "" {
		gap = " "
	}
	body := strings.TrimLeft(rest, " \t")

	comment := ""
	if value, c := splitTrailingComment(body); c != "" {
		comment = c
		body = value
	}

	quote := ""
	if len(body) > 0 && (body[0] == '"' || body[0] == '\'') {
		quote = string(body[0])
	}

	return head + gap + quote + target + quote + comment
}

// RewriteComposeFile applies want to the compose file at path, first copying
// the original to path+".bak". The backup is the local safety net; the
// authoritative copy is the restic snapshot taken before the job started.
func RewriteComposeFile(path string, want map[string]string) ([]string, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	updated, missing := RewriteImages(string(original), want)
	if len(missing) == len(want) {
		// Nothing matched: do not touch the file at all.
		return missing, nil
	}
	info, err := os.Stat(path)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path+".bak", original, mode); err != nil {
		return nil, fmt.Errorf("write %s.bak: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return missing, nil
}

func isBlankOrComment(s string) bool {
	t := strings.TrimSpace(s)
	return t == "" || strings.HasPrefix(t, "#")
}

func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			// Tabs are invalid for YAML indentation; count as one so the
			// document still parses predictably instead of panicking.
			n++
		default:
			return n
		}
	}
	return n
}

// splitKey splits "key: value" (or a bare "key:") into its parts. It refuses
// list items and anything without a colon so sequence entries never look like
// service names.
func splitKey(trimmed string) (key, value string, ok bool) {
	if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
		return "", "", false
	}
	i := strings.Index(trimmed, ":")
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:i])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	return key, strings.TrimSpace(trimmed[i+1:]), true
}

// splitTrailingComment separates a trailing ` # comment`, honouring quotes so a
// '#' inside an image tag is not mistaken for one.
func splitTrailingComment(s string) (value, comment string) {
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			if c == inQuote {
				inQuote = 0
			}
		case c == '"' || c == '\'':
			inQuote = c
		case c == '#' && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t'):
			// Keep the whole whitespace run so alignment survives a rewrite.
			j := i
			for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
				j--
			}
			return s[:j], s[j:]
		}
	}
	return s, ""
}

func stripComment(s string) string {
	v, _ := splitTrailingComment(s)
	return v
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
