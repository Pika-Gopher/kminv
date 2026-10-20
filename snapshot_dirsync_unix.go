//go:build !windows

package kminv

import "os"

func syncSnapshotDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
