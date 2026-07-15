package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// legacyMacLabels are old launchd plist basenames from prior naming — removed on
// install so a re-install doesn't leave a second timer collecting in parallel.
// "co.wimcorp.*" used a wrong reverse-DNS (wimcorp.co, not wimcorp.co.kr).
var legacyMacLabels = []string{
	"co.wimcorp.wim-backoffice-prompt-agent.plist",
	"co.wimcorp.promptagent.plist",
}

// InstallMac writes a launchd plist to ~/Library/LaunchAgents/ and loads it.
// The agent runs wim-backoffice-prompt-agent run-once every interval seconds.
// logPath captures the daemon's stdout/stderr (StandardOutPath/StandardErrorPath)
// — launchd otherwise discards it, so a scheduled run's panics/output would be
// lost. Interactive diagnostics go through the agentlog file at the same path.
func InstallMac(execPath string, interval int, logPath string) error {
	home, _ := os.UserHomeDir()

	// Remove any legacy-labeled plist first (best-effort) to avoid double-running.
	for _, name := range legacyMacLabels {
		old := filepath.Join(home, "Library/LaunchAgents", name)
		_ = exec.Command("launchctl", "unload", old).Run()
		_ = os.Remove(old)
	}

	plist := filepath.Join(home, "Library/LaunchAgents/kr.co.wimcorp.wim-backoffice-prompt-agent.plist")

	if err := os.WriteFile(plist, []byte(macPlist(execPath, interval, logPath)), 0o644); err != nil {
		return err
	}
	return exec.Command("launchctl", "load", plist).Run()
}

// macPlist builds the launchd plist body (pure — no I/O, unit-tested).
func macPlist(execPath string, interval int, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>kr.co.wimcorp.wim-backoffice-prompt-agent</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run-once</string></array>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>%[3]s</string>
  <key>StandardErrorPath</key><string>%[3]s</string>
</dict></plist>`, xmlEscape(execPath), interval, xmlEscape(logPath))
}

// xmlEscape escapes the XML character-data metacharacters so paths containing
// & < > can't break the plist. Home-based paths rarely contain these, but a
// malformed plist would silently fail to load, so escape defensively.
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// UninstallMac unloads and removes the launchd plist.
func UninstallMac() error {
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library/LaunchAgents/kr.co.wimcorp.wim-backoffice-prompt-agent.plist")
	_ = exec.Command("launchctl", "unload", plist).Run()
	return os.Remove(plist)
}
