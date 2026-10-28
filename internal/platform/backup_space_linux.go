package platform

import (
	"errors"
	"syscall"
)

func backupSpace(path string, needed uint64) error {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return e
	}
	if s.Bavail*uint64(s.Bsize) < needed+256*1024*1024 || s.Ffree < 10000 {
		return errors.New("insufficient physical backup headroom or free inodes")
	}
	return nil
}
