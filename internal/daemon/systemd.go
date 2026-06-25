package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// InstallLinux installs a systemd --user service + timer that runs
// wim-prompt-agent run-once every intervalSec seconds.
// loginctl enable-linger ensures the timer fires even when the user is logged out.
func InstallLinux(execPath string, intervalSec int) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config/systemd/user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	svc := fmt.Sprintf(`[Unit]
Description=WIM prompt agent
[Service]
Type=oneshot
ExecStart=%s run-once
`, execPath)

	timer := fmt.Sprintf(`[Unit]
Description=WIM prompt agent timer
[Timer]
OnBootSec=60
OnUnitActiveSec=%d
[Install]
WantedBy=timers.target
`, intervalSec)

	if err := os.WriteFile(filepath.Join(dir, "wim-prompt-agent.service"), []byte(svc), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "wim-prompt-agent.timer"), []byte(timer), 0o644); err != nil {
		return err
	}

	// Enable linger so --user timers fire after logout (self target, no root needed).
	_ = exec.Command("loginctl", "enable-linger").Run()

	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return err
	}
	return exec.Command("systemctl", "--user", "enable", "--now", "wim-prompt-agent.timer").Run()
}

// UninstallLinux stops and removes the systemd --user timer and service.
func UninstallLinux() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", "wim-prompt-agent.timer").Run()
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config/systemd/user")
	_ = os.Remove(filepath.Join(dir, "wim-prompt-agent.timer"))
	_ = os.Remove(filepath.Join(dir, "wim-prompt-agent.service"))
	return nil
}
