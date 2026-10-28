package platform

import (
	"archive/tar"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const backupLimit int64 = 512 << 20
const backupChunk = 64 << 10
const backupMagic = "KZUREBK1"

// Each bounded record authenticates its position and backup identity. An empty
// final record is mandatory, so truncation at a record boundary is an error.
func encryptBackup(dst io.Writer, src io.Reader, key []byte, identity string) error {
	block, e := aes.NewCipher(key)
	if e != nil {
		return e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	prefix := make([]byte, 8)
	if _, e = rand.Read(prefix); e != nil {
		return e
	}
	if _, e = dst.Write(append([]byte(backupMagic), prefix...)); e != nil {
		return e
	}
	buf := make([]byte, backupChunk)
	var total int64
	for counter := uint32(0); ; counter++ {
		n, err := io.ReadFull(src, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		total += int64(n)
		if total > backupLimit {
			return errors.New("backup exceeds the 512 MiB archive limit")
		}
		nonce := append(append([]byte{}, prefix...), make([]byte, 4)...)
		binary.BigEndian.PutUint32(nonce[8:], counter)
		sealed := aead.Seal(nil, nonce, buf[:n], []byte(identity))
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(sealed)))
		if _, e = dst.Write(length[:]); e != nil {
			return e
		}
		if _, e = dst.Write(sealed); e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
	}
}

func decryptBackup(dst io.Writer, src io.Reader, key []byte, identity string) error {
	block, e := aes.NewCipher(key)
	if e != nil {
		return e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return e
	}
	header := make([]byte, 16)
	if _, e = io.ReadFull(src, header); e != nil {
		return e
	}
	if string(header[:8]) != backupMagic {
		return errors.New("unsupported backup format")
	}
	var total int64
	for counter := uint32(0); ; counter++ {
		var length [4]byte
		if _, e = io.ReadFull(src, length[:]); e != nil {
			return errors.New("backup is truncated")
		}
		n := binary.BigEndian.Uint32(length[:])
		if n < uint32(aead.Overhead()) || n > backupChunk+uint32(aead.Overhead()) {
			return errors.New("invalid encrypted record")
		}
		sealed := make([]byte, n)
		if _, e = io.ReadFull(src, sealed); e != nil {
			return e
		}
		nonce := append(append([]byte{}, header[8:]...), make([]byte, 4)...)
		binary.BigEndian.PutUint32(nonce[8:], counter)
		plain, err := aead.Open(nil, nonce, sealed, []byte(identity))
		if err != nil {
			return errors.New("backup authentication failed")
		}
		total += int64(len(plain))
		if total > backupLimit {
			return errors.New("backup archive is too large")
		}
		if len(plain) == 0 {
			var b [1]byte
			n, err := src.Read(b[:])
			if n != 0 || err != io.EOF {
				return errors.New("backup has trailing data")
			}
			return nil
		}
		if _, e = dst.Write(plain); e != nil {
			return e
		}
	}
}

func packBackup(dst io.Writer, source string) error {
	root, e := os.OpenRoot(source)
	if e != nil {
		return e
	}
	defer root.Close()
	tw := tar.NewWriter(dst)
	var size int64
	entries := 0
	e = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > 100000 {
			return errors.New("backup exceeds 100000 entries")
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		// Refuse links and special files instead of silently omitting data or
		// following a PostgreSQL tablespace outside the managed volume.
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("backup requires regular files and directories: unsupported entry %q", name)
		}
		size += info.Size()
		if size > backupLimit {
			return errors.New("backup exceeds 512 MiB")
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = name
		if !info.IsDir() && h.Mode&07000 != 0 {
			return errors.New("backup refuses special permission bits on regular files")
		}
		if err = tw.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		_, err = io.CopyN(tw, f, info.Size())
		f.Close()
		return err
	})
	if e != nil {
		return e
	}
	return tw.Close()
}

func unpackBackup(source io.Reader, destination string) error {
	root, e := os.OpenRoot(destination)
	if e != nil {
		return e
	}
	defer root.Close()
	entries, e := os.ReadDir(destination)
	if e != nil {
		return e
	}
	if len(entries) != 0 {
		return errors.New("restore requires an empty destination volume")
	}
	tr := tar.NewReader(source)
	type directory struct{ h tar.Header }
	dirs := []directory{}
	seen := map[string]bool{}
	var size int64
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if count >= 100000 {
			return errors.New("too many archive entries")
		}
		name := h.Name
		if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || seen[name] {
			return errors.New("unsafe or duplicate archive path")
		}
		seen[name] = true
		if (h.Typeflag != tar.TypeDir && h.Mode&07000 != 0) || h.Size < 0 {
			return errors.New("unsupported archive permissions or size")
		}
		size += h.Size
		if size > backupLimit {
			return errors.New("restore exceeds archive limit")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if e = root.MkdirAll(name, 0700); e != nil {
				return e
			}
			dirs = append(dirs, directory{*h})
		case tar.TypeReg, tar.TypeRegA:
			if name == "." {
				return errors.New("invalid regular root entry")
			}
			if e = root.MkdirAll(filepath.Dir(name), 0700); e != nil {
				return e
			}
			f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, tr, h.Size)
			if err == nil {
				err = f.Chown(h.Uid, h.Gid)
			}
			if err == nil {
				err = f.Chmod(os.FileMode(h.Mode))
			}
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if err = root.Chtimes(name, h.AccessTime, h.ModTime); err != nil {
				return err
			}
		default:
			return errors.New("archive contains an unsupported link or special file")
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		h := dirs[i].h
		if e = root.Chown(h.Name, h.Uid, h.Gid); e != nil {
			return e
		}
		if e = root.Chmod(h.Name, backupFileMode(h.Mode)); e != nil {
			return e
		}
		if e = root.Chtimes(h.Name, h.AccessTime, h.ModTime); e != nil {
			return e
		}
	}
	if !seen["."] {
		return errors.New("archive is missing its root directory")
	}
	return nil
}

func backupFileMode(mode int64) os.FileMode {
	p := os.FileMode(mode & 0777)
	if mode&01000 != 0 {
		p |= os.ModeSticky
	}
	if mode&02000 != 0 {
		p |= os.ModeSetgid
	}
	if mode&04000 != 0 {
		p |= os.ModeSetuid
	}
	return p
}
