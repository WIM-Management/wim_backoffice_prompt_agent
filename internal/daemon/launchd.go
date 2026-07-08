package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
func InstallMac(execPath string, interval int) error {
	home, _ := os.UserHomeDir()

	// Remove any legacy-labeled plist first (best-effort) to avoid double-running.
	for _, name := range legacyMacLabels {
		old := filepath.Join(home, "Library/LaunchAgents", name)
		_ = exec.Command("launchctl", "unload", old).Run()
		_ = os.Remove(old)
	}

	plist := filepath.Join(home, "Library/LaunchAgents/kr.co.wimcorp.wim-backoffice-prompt-agent.plist")

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>kr.co.wimcorp.wim-backoffice-prompt-agent</string>
  <key>ProgramArguments</key><array><string>%s</string><string>run-once</string></array>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
</dict></plist>`, execPath, interval)

	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		return err
	}
	return exec.Command("launchctl", "load", plist).Run()
}

// UninstallMac unloads and removes the launchd plist.
func UninstallMac() error {
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library/LaunchAgents/kr.co.wimcorp.wim-backoffice-prompt-agent.plist")
	_ = exec.Command("launchctl", "unload", plist).Run()
	return os.Remove(plist)
}
