package daemon

import (
	"strings"
	"testing"
)

func TestMacPlistIncludesLogPaths(t *testing.T) {
	p := macPlist("/usr/local/bin/agent", 900, "/Users/x/.wim-backoffice-prompt-agent/agent.log")

	for _, want := range []string{
		"<key>StandardOutPath</key><string>/Users/x/.wim-backoffice-prompt-agent/agent.log</string>",
		"<key>StandardErrorPath</key><string>/Users/x/.wim-backoffice-prompt-agent/agent.log</string>",
		"<key>StartInterval</key><integer>900</integer>",
		"<string>/usr/local/bin/agent</string><string>run-once</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q\n---\n%s", want, p)
		}
	}
}

func TestMacPlistEscapesMetachars(t *testing.T) {
	p := macPlist("/opt/a&b/agent", 60, "/log/<weird>&path.log")

	if strings.Contains(p, "a&b") || strings.Contains(p, "<weird>") {
		t.Errorf("unescaped metacharacters leaked into plist:\n%s", p)
	}
	if !strings.Contains(p, "a&amp;b") || !strings.Contains(p, "&lt;weird&gt;&amp;path") {
		t.Errorf("expected escaped entities not found:\n%s", p)
	}
}
