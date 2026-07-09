//go:build windows

package enroll

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// KeychainStore stores the device token as a DPAPI-encrypted file
// (~/.wim-backoffice-prompt-agent/device-token.dpapi). DPAPI(CryptProtectData)는
// 현재 Windows 사용자 계정에 바인딩되므로 다른 계정/머신에서는 복호화되지 않는다.
// darwin(security keychain)·linux(0600 파일)처럼 외부 도구를 요구하지 않는 stdlib-only 경로.
type KeychainStore struct{ path string }

func NewKeychainStore() *KeychainStore {
	home, _ := os.UserHomeDir()
	return &KeychainStore{filepath.Join(home, ".wim-backoffice-prompt-agent", "device-token.dpapi")}
}

func (k *KeychainStore) Set(v string) error {
	enc, err := dpapiEncrypt([]byte(v))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(k.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(k.path, enc, 0o600)
}

func (k *KeychainStore) Get() (string, error) {
	enc, err := os.ReadFile(k.path)
	if err != nil {
		return "", err
	}
	dec, err := dpapiDecrypt(enc)
	if err != nil {
		return "", err
	}
	return string(dec), nil
}

// --- DPAPI (crypt32.dll) ---

const cryptprotectUIForbidden = 0x1

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procProtectData   = crypt32.NewProc("CryptProtectData")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree     = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) *dataBlob {
	if len(d) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b *dataBlob) copyAndFree() []byte {
	defer procLocalFree.Call(uintptr(unsafe.Pointer(b.pbData))) //nolint:errcheck // best-effort free
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

func dpapiEncrypt(data []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procProtectData.Call(
		uintptr(unsafe.Pointer(newBlob(data))), // pDataIn
		0, 0, 0, 0,                             // szDataDescr, pOptionalEntropy, pvReserved, pPromptStruct
		cryptprotectUIForbidden,       // dwFlags
		uintptr(unsafe.Pointer(&out)), // pDataOut
	)
	if r == 0 {
		return nil, err
	}
	return out.copyAndFree(), nil
}

func dpapiDecrypt(data []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procUnprotectData.Call(
		uintptr(unsafe.Pointer(newBlob(data))), // pDataIn
		0, 0, 0, 0,                             // ppszDataDescr, pOptionalEntropy, pvReserved, pPromptStruct
		cryptprotectUIForbidden,       // dwFlags
		uintptr(unsafe.Pointer(&out)), // pDataOut
	)
	if r == 0 {
		return nil, err
	}
	return out.copyAndFree(), nil
}
