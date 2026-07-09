//go:build linux

package enroll

import (
	"os"
	"path/filepath"
	"strings"
)

// KeychainStore stores the device token as a 0600 file
// (~/.wim-backoffice-prompt-agent/device-token). 리눅스엔 DPAPI(Windows)·login
// keychain(darwin) 같은 OS 바인딩 암호화가 없고, 예전 secret-tool(libsecret)
// 경로는 실행 중인 secret service(gnome-keyring + D-Bus 세션)를 요구해 헤드리스
// 서버·SSH·WSL·컨테이너에서 아예 못 썼다. 그래서 외부도구 없이 stdlib만으로
// 동작하도록 파일 저장으로 전환한다 — ~/.aws/credentials·gh·kubeconfig와 같은
// 신뢰 모델(사용자 홈, 0600). 수집 토큰은 사용자 권한으로 도는 에이전트가 쓰는
// 값이라 이 수준이 실용적 타협점이다.
type KeychainStore struct{ path string }

func NewKeychainStore() *KeychainStore {
	home, _ := os.UserHomeDir()
	return &KeychainStore{filepath.Join(home, ".wim-backoffice-prompt-agent", "device-token")}
}

func (k *KeychainStore) Set(v string) error {
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(k.path, []byte(v), 0o600)
}

func (k *KeychainStore) Get() (string, error) {
	out, err := os.ReadFile(k.path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
