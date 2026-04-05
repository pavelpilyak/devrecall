package ticketlink

import (
	"regexp"
	"strings"
)

// issueKeyRe matches issue keys like PROJ-123, ENG-456, AB-1.
// Must be uppercase letters followed by a dash and digits.
var issueKeyRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]+-\d+)\b`)

// branchKeyRe matches issue keys in branch names where they may be lowercase,
// e.g., "eng-456-fix-auth" or "feature/proj-123-update".
var branchKeyRe = regexp.MustCompile(`(?i)\b([A-Z][A-Z0-9]+-\d+)\b`)

// ExtractFromMessage extracts issue keys from a commit message.
// Returns deduplicated, uppercase keys. e.g., ["PROJ-123", "ENG-456"].
func ExtractFromMessage(message string) []string {
	matches := issueKeyRe.FindAllString(message, -1)
	return dedupe(matches)
}

// ExtractFromBranch extracts issue keys from a branch name.
// Handles lowercase keys (e.g., "eng-456-fix-auth" → "ENG-456").
// Returns deduplicated, uppercase keys.
func ExtractFromBranch(branch string) []string {
	matches := branchKeyRe.FindAllStringSubmatch(branch, -1)
	var keys []string
	for _, m := range matches {
		keys = append(keys, strings.ToUpper(m[1]))
	}
	return dedupe(keys)
}

// Extract extracts issue keys from both a commit message and branch name,
// returning a deduplicated, uppercase list.
func Extract(message, branch string) []string {
	var all []string
	all = append(all, ExtractFromMessage(message)...)
	all = append(all, ExtractFromBranch(branch)...)
	return dedupe(all)
}

func dedupe(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(keys))
	var result []string
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			result = append(result, k)
		}
	}
	return result
}
