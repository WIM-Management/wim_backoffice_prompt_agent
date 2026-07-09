//go:build darwin

package enroll

import (
	"errors"
	"os/exec"
	"strings"
)

// errSecItemNotFound: `security find-generic-password`가 항목이 없을 때 반환하는
// 종료 코드(44). 미등록(정상)이므로 raw 에러가 아니라 빈 토큰으로 취급한다.
const errSecItemNotFound = 44

// KeychainStore stores a device token in the macOS login keychain, keyed by
// account (key). service는 고정, account=key로 폴더별 토큰을 분리한다.
type KeychainStore struct{ service, account string }

func NewKeychainStore(key string) *KeychainStore {
	return &KeychainStore{"wim-backoffice-prompt-agent", key}
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
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == errSecItemNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Delete removes the keychain item. 미존재(exit 44)는 성공으로 간주(멱등).
func (k *KeychainStore) Delete() error {
	err := exec.Command("security", "delete-generic-password", "-s", k.service, "-a", k.account).Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == errSecItemNotFound {
		return nil
	}
	return err
}
