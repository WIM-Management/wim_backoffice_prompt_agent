// Package redact scrubs secrets from strings before upload.
package redact

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[a-zA-Z0-9-]{20,}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*KEY-----.*?-----END [A-Z ]*KEY-----`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`ya29\.[0-9A-Za-z_\-]+`),
	regexp.MustCompile(`1//[0-9A-Za-z_\-]+`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]+`),
	regexp.MustCompile(`(?i)(api[_\-]?key|secret|token|password)\s*=\s*\S+`),
}

// Scrub replaces known secret patterns with «REDACTED».
func Scrub(s string) string {
	for _, p := range patterns {
		s = p.ReplaceAllString(s, "«REDACTED»")
	}
	return s
}
