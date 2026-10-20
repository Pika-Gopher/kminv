//go:build !windows

package kminv

import "os"

func replaceSnapshot(path, tempPath string) error {
	return os.Rename(tempPath, path)
}
