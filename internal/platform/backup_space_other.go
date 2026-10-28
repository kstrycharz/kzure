//go:build !linux

package platform

import "errors"

func backupSpace(path string, needed uint64) error {
	return errors.New("backup helpers require a Linux Docker engine")
}
