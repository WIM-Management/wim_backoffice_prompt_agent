package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// InstallMac writes a launchd plist to ~/Library/LaunchAgents/ and loads it.
// The agent runs wim-prompt-agent run-once every interval seconds.
func InstallMac(execPath string, interval int) error {
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library/LaunchAgents/co.wimcorp.promptagent.plist")

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>co.wimcorp.promptagent</string>
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
	plist := filepath.Join(home, "Library/LaunchAgents/co.wimcorp.promptagent.plist")
	_ = exec.Command("launchctl", "unload", plist).Run()
	return os.Remove(plist)
}
