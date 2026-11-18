//go:build windows

package app

import (
	"syscall"
	"unsafe"
)

var (
	kernel32DLL           = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceW = kernel32DLL.NewProc("GetDiskFreeSpaceExW")
)

func diskUsage(path string) (freeBytes int64, totalBytes int64) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var freeAvail, total, totalFree uint64
	r1, _, _ := procGetDiskFreeSpaceW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeAvail)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r1 == 0 {
		return 0, 0
	}
	return int64(freeAvail), int64(total)
}
