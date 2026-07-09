//go:build linux

package enroll

import (
	"os"
	"path/filepath"
	"strings"
)

// KeychainStore stores a device token as a 0600 file
// (~/.wim-backoffice-prompt-agent/<key>). 리눅스엔 DPAPI(Windows)·login
// keychain(darwin) 같은 OS 바인딩 암호화가 없고, 예전 secret-tool(libsecret)
// 경로는 실행 중인 secret service(gnome-keyring + D-Bus 세션)를 요구해 헤드리스
// 서버·SSH·WSL·컨테이너에서 아예 못 썼다. 그래서 외부도구 없이 stdlib만으로
// 동작하도록 파일 저장으로 전환한다 — ~/.aws/credentials·gh·kubeconfig와 같은
// 신뢰 모델(사용자 홈, 0600). key로 폴더별 토큰을 분리한다(기본=device-token).
type KeychainStore struct{ path string }

func NewKeychainStore(key string) *KeychainStore {
	home, _ := os.UserHomeDir()
	return &KeychainStore{filepath.Join(home, ".wim-backoffice-prompt-agent", key)}
}

func (k *KeychainStore) Set(v string) error {
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(k.path, []byte(v), 0o600)
}

func (k *KeychainStore) Get() (string, error) {
	// 파일 없음 = 미등록(정상) — 빈 토큰으로 조용히 반환(조회 실패로 안 찍음).
	out, err := os.ReadFile(k.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Delete removes the token file. 미존재는 성공으로 간주(멱등).
func (k *KeychainStore) Delete() error {
	if err := os.Remove(k.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
