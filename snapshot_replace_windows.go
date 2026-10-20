//go:build windows

package kminv

import (
	"syscall"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	replaceFileProc = kernel32.NewProc("ReplaceFileW")
	moveFileProc    = kernel32.NewProc("MoveFileExW")
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
	errorFileNotFound       = syscall.Errno(2)
)

func replaceSnapshot(path, tempPath string) error {
	target, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	replacement, err := syscall.UTF16PtrFromString(tempPath)
	if err != nil {
		return err
	}

	// ReplaceFileW preserves the published destination if replacement fails.
	r1, _, callErr := replaceFileProc.Call(
		uintptr(unsafe.Pointer(target)),
		uintptr(unsafe.Pointer(replacement)),
		0,
		0,
		0,
		0,
	)
	if r1 != 0 {
		return nil
	}
	if errno, ok := callErr.(syscall.Errno); !ok || errno != errorFileNotFound {
		return callErr
	}

	r1, _, callErr = moveFileProc.Call(
		uintptr(unsafe.Pointer(replacement)),
		uintptr(unsafe.Pointer(target)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if r1 == 0 {
		return callErr
	}
	return nil
}
