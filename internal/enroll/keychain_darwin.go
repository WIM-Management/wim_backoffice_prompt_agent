//go:build darwin

package enroll

import (
	"os/exec"
	"strings"
)

// KeychainStore stores the device token in the macOS login keychain.
type KeychainStore struct{ service, account string }

func NewKeychainStore() *KeychainStore {
	return &KeychainStore{"wim-prompt-agent", "device-token"}
}

func (k *KeychainStore) Set(v string) error {
	// best-effort delete: errors when no existing entry (first enroll) — ignored.
	_ = exec.Command("security", "delete-generic-password", "-s", k.service, "-a", k.account).Run()
	// NOTE: `security` CLI only accepts the secret via the `-w` argument (no stdin),
	// so the token is briefly visible in the process arg list. This is the documented
	// limitation of wrapping `security`; switch to go-keychain if that becomes unacceptable.
	return exec.Command("security", "add-generic-password", "-s", k.service, "-a", k.account, "-w", v).Run()
}

func (k *KeychainStore) Get() (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", k.service, "-a", k.account, "-w").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
