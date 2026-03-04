//go:build windows

package store

import (
	"syscall"
	"unsafe"
)

func replaceFile(tmpPath, finalPath string) error {
	from, err := syscall.UTF16PtrFromString(tmpPath)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(finalPath)
	if err != nil {
		return err
	}

	const (
		movefileReplaceExisting = 0x1
		movefileWriteThrough    = 0x8
	)
	k32 := syscall.NewLazyDLL("kernel32.dll")
	proc := k32.NewProc("MoveFileExW")
	r1, _, e1 := proc.Call(
		uintptr(unsafe.Pointer(from)),
		uintptr(unsafe.Pointer(to)),
		uintptr(movefileReplaceExisting|movefileWriteThrough),
	)
	if r1 == 0 {
		if e1 != nil && e1 != syscall.Errno(0) {
			return e1
		}
		return syscall.EINVAL
	}
	return nil
}
