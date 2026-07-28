package selfupdate

import "syscall"

// hideFile sets FILE_ATTRIBUTE_HIDDEN so the ".<base>.old" leftover of an update
// does not sit visibly next to the binary until the next launch sweeps it. Windows
// keeps the replaced executable locked while the process that started from it is
// still running, so it cannot simply be deleted here.
func hideFile(path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN)
}
