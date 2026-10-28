package platform

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
)

var backupArtifactPattern = regexp.MustCompile(`^[0-9a-f]{32}-[1-9][0-9]{0,5}$`)

type backupResult struct {
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Image    string `json:"image"`
}

// RunBackupTool is deliberately independent of management credentials and DB.
// Only a per-backup data key reaches this short-lived, networkless helper.
func RunBackupTool(mode, artifact string) error {
	if !backupArtifactPattern.MatchString(artifact) {
		return errors.New("invalid backup artifact")
	}
	if mode == "backup-delete" {
		for _, suffix := range []string{".kzb", ".kzb.partial"} {
			if e := os.Remove("/backups/" + artifact + suffix); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		return nil
	}
	key, e := base64.StdEncoding.DecodeString(os.Getenv("KZURE_BACKUP_KEY"))
	if e != nil || len(key) != 32 {
		return errors.New("invalid backup data key")
	}
	file := "/backups/" + artifact + ".kzb"
	if mode == "backup-pack" {
		if e = backupSpace("/backups", uint64(backupLimit+1024*1024)); e != nil {
			return e
		}
		f, err := os.OpenFile(file+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer os.Remove(file + ".partial")
		hash := sha256.New()
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(packBackup(pw, "/source")) }()
		err = encryptBackup(io.MultiWriter(f, hash), pr, key, artifact)
		pr.CloseWithError(err)
		if err == nil {
			err = f.Sync()
		}
		info, statErr := f.Stat()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err = os.Rename(file+".partial", file); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(backupResult{Artifact: artifact, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: info.Size()})
	}
	if mode != "backup-unpack" {
		return errors.New("invalid backup mode")
	}
	if e = backupSpace("/target", uint64(backupLimit)); e != nil {
		return e
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(f, backupLimit+1024*1024))
	if e != nil {
		return e
	}
	if n > backupLimit+512*1024 || hex.EncodeToString(hash.Sum(nil)) != os.Getenv("KZURE_BACKUP_SHA256") {
		return errors.New("backup checksum mismatch")
	}
	// Authenticate the entire archive before creating even one restored file.
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	if e = decryptBackup(io.Discard, f, key, artifact); e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { err := decryptBackup(pw, f, key, artifact); pw.CloseWithError(err); done <- err }()
	e = unpackBackup(pr, "/target")
	if e == nil {
		_, e = io.Copy(io.Discard, pr)
	}
	pr.CloseWithError(e)
	decryptErr := <-done
	if e != nil {
		return e
	}
	if decryptErr != nil {
		return decryptErr
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"restored": true})
}
