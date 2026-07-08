//go:build linux

package enroll

import (
	"os/exec"
	"strings"
)

// KeychainStore stores the device token via libsecret (secret-tool).
// Requires libsecret-tools + a running secret service (e.g. gnome-keyring).
type KeychainStore struct{ service, account string }

func NewKeychainStore() *KeychainStore {
	return &KeychainStore{"wim-backoffice-prompt-agent", "device-token"}
}

func (k *KeychainStore) Set(v string) error {
	cmd := exec.Command("secret-tool", "store", "--label=wim-backoffice-prompt-agent",
		"service", k.service, "account", k.account)
	cmd.Stdin = strings.NewReader(v)
	return cmd.Run()
}

func (k *KeychainStore) Get() (string, error) {
	out, err := exec.Command("secret-tool", "lookup", "service", k.service, "account", k.account).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
