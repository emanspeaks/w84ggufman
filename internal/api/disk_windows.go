//go:build windows

package api

import (
	"syscall"
	"unsafe"
)

var (
	modKernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = modKernel32.NewProc("GetDiskFreeSpaceExW")
)

func getDiskInfo(path string) (diskInfo, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return diskInfo{}, err
	}
	var freeBytesAvailableToCaller, totalBytes, totalFreeBytes uint64
	r, _, e := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailableToCaller)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if r == 0 {
		return diskInfo{}, e
	}
	return diskInfo{TotalBytes: totalBytes, FreeBytes: totalFreeBytes, UsedBytes: totalBytes - totalFreeBytes}, nil
}
