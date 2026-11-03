// SPDX-License-Identifier: GPL-3.0-or-later
//go:build windows

package store

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func lockFile(file *os.File) error {
	overlapped := new(syscall.Overlapped)
	result, _, err := lockFileEx.Call(
		file.Fd(),
		uintptr(0x1|0x2), // LOCKFILE_FAIL_IMMEDIATELY | LOCKFILE_EXCLUSIVE_LOCK
		0,
		uintptr(^uint32(0)),
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result == 0 {
		if errors.Is(err, syscall.Errno(33)) { // ERROR_LOCK_VIOLATION
			return errLockBusy
		}
		return err
	}
	return nil
}
