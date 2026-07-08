//go:build !windows

package updater

import "os"

// replaceBinary atomically swaps the running binary. On unix, renaming over the
// executing file is safe (the running process keeps the old inode); the next
// daemon cycle runs the new binary.
func replaceBinary(tmpPath, execPath string) error {
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpPath, execPath)
}
