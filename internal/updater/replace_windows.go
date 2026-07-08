//go:build windows

package updater

import "os"

// replaceBinary swaps the binary on Windows, where a running .exe cannot be
// removed: rename the live exe to .old, then move the new one into place.
// The stale .old is cleaned best-effort on the next update.
func replaceBinary(tmpPath, execPath string) error {
	old := execPath + ".old"
	_ = os.Remove(old) // 이전 업데이트 잔재 정리(best-effort)
	if err := os.Rename(execPath, old); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		_ = os.Rename(old, execPath) // 롤백
		return err
	}
	return nil
}
