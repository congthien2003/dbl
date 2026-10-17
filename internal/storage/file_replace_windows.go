//go:build windows

package storage

import (
	"syscall"
	"unsafe"
)

var sqlKernel32 = syscall.NewLazyDLL("kernel32.dll")
var sqlReplaceFile = sqlKernel32.NewProc("ReplaceFileW")
var sqlMoveFile = sqlKernel32.NewProc("MoveFileExW")

func replaceSQLFile(tmp, target string, exists bool) (bool, error) {
	from, err := syscall.UTF16PtrFromString(tmp)
	if err != nil {
		return false, err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return false, err
	}
	var result uintptr
	if exists {
		result, _, err = sqlReplaceFile.Call(uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), 0, 0, 0, 0)
	} else {
		// WRITE_THROUGH without REPLACE_EXISTING: a newly appeared file is a conflict.
		result, _, err = sqlMoveFile.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), 0x8)
	}
	if result == 0 {
		return false, err
	}
	return true, nil
}
