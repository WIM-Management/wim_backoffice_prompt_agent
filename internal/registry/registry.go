// Package registry tracks which Claude config directories are enrolled for
// collection, each with its own device-token key. Enrollment is explicit only —
// directories are never auto-discovered (오수집·프라이버시 방지).
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultTokenKey is the token key for the default ~/.claude directory. Kept as
// "device-token" for byte-identical backward compatibility with single-dir installs.
const DefaultTokenKey = "device-token"

// Entry is one enrolled config directory and its device-token key.
type Entry struct {
	ConfigDir string `json:"configDir"`
	TokenKey  string `json:"tokenKey"`
}

// DefaultConfigDir returns ~/.claude.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// IsDefault reports whether dir is the default ~/.claude (cleaned compare).
func IsDefault(dir string) bool {
	return filepath.Clean(dir) == filepath.Clean(DefaultConfigDir())
}

// Slug derives a token/path-safe id from a config dir basename: leading dots
// stripped, lowercased, runs of non-[a-z0-9] collapsed to '-'.
// e.g. "/home/u/.claude-melle" -> "claude-melle".
func Slug(configDir string) string {
	base := strings.TrimLeft(filepath.Base(filepath.Clean(configDir)), ".")
	base = strings.ToLower(base)
	var b strings.Builder
	prevDash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "claude"
	}
	return s
}

// TokenKeyFor returns the token key for a config dir: the default key for
// ~/.claude, else "device-token-<slug>".
func TokenKeyFor(configDir string) string {
	if IsDefault(configDir) {
		return DefaultTokenKey
	}
	return DefaultTokenKey + "-" + Slug(configDir)
}

// PrimaryEntry returns the primary (default ~/.claude) entry from the list —
// the one whose token attributes machine-wide tool sources (~/.codex).
// Returns ok=false if no default entry is present.
func PrimaryEntry(entries []Entry) (Entry, bool) {
	for _, e := range entries {
		if IsDefault(e.ConfigDir) {
			return e, true
		}
	}
	return Entry{}, false
}

// Registry persists enrolled entries to a JSON file.
type Registry struct{ path string }

func New(path string) *Registry { return &Registry{path: path} }

// loadRaw returns entries exactly as stored (nil if the file is absent).
func (r *Registry) loadRaw() ([]Entry, error) {
	b, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, err
	}
	return es, nil
}

// List returns the entries to collect. The default ~/.claude is always included
// (prepended if absent) so it is collected regardless of registry contents —
// this also makes a missing registry file behave like a single-dir install.
func (r *Registry) List() ([]Entry, error) {
	es, err := r.loadRaw()
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if IsDefault(e.ConfigDir) {
			return es, nil
		}
	}
	return append([]Entry{{ConfigDir: DefaultConfigDir(), TokenKey: DefaultTokenKey}}, es...), nil
}

func (r *Registry) save(es []Entry) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(es, "", "  ")
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Upsert adds (or refreshes) an entry keyed by ConfigDir and returns it. It
// rejects a token-key collision from a *different* directory (two dirs whose
// basenames slug to the same key).
func (r *Registry) Upsert(configDir string) (Entry, error) {
	configDir = filepath.Clean(configDir)
	tokenKey := TokenKeyFor(configDir)
	es, err := r.loadRaw()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range es {
		if e.TokenKey == tokenKey && filepath.Clean(e.ConfigDir) != configDir {
			return Entry{}, fmt.Errorf("토큰 키 %q가 기존 폴더 %q와 충돌합니다(다른 폴더명을 쓰세요)", tokenKey, e.ConfigDir)
		}
	}
	for i, e := range es {
		if filepath.Clean(e.ConfigDir) == configDir {
			es[i].TokenKey = tokenKey
			return es[i], r.save(es)
		}
	}
	ne := Entry{ConfigDir: configDir, TokenKey: tokenKey}
	return ne, r.save(append(es, ne))
}

// Remove deletes an entry by ConfigDir, returning it (for token cleanup) and
// whether it existed. The default ~/.claude cannot be removed.
func (r *Registry) Remove(configDir string) (Entry, bool, error) {
	configDir = filepath.Clean(configDir)
	if IsDefault(configDir) {
		return Entry{}, false, fmt.Errorf("기본 폴더(~/.claude)는 forget할 수 없습니다")
	}
	es, err := r.loadRaw()
	if err != nil {
		return Entry{}, false, err
	}
	var removed Entry
	var found bool
	kept := make([]Entry, 0, len(es))
	for _, e := range es {
		if filepath.Clean(e.ConfigDir) == configDir {
			removed, found = e, true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return Entry{}, false, nil
	}
	return removed, true, r.save(kept)
}
