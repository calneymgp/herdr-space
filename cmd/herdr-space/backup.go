package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"herdr-space/internal/auth"
	"herdr-space/internal/store"
)

var bundleName = regexp.MustCompile(`^space-(\d{4}-\d{2}-\d{2})-\d{6}\.\d{9}-[0-9a-f]{32}\.bundle$`)

func runBackup(dataDir string, retain int) (string, error) {
	if retain < 1 || retain > 365 {
		return "", errors.New("invalid backup retention")
	}
	keyPath := filepath.Join(dataDir, "auth.key")
	databasePath := filepath.Join(dataDir, "space.db")
	databaseInfo, err := os.Lstat(databasePath)
	if err != nil {
		return "", err
	}
	if !databaseInfo.Mode().IsRegular() {
		return "", store.ErrInvalid
	}
	keyInfo, err := os.Lstat(keyPath)
	if err != nil {
		return "", err
	}
	if !keyInfo.Mode().IsRegular() {
		return "", store.ErrInvalid
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return "", err
	}
	if len(key) != 32 {
		return "", store.ErrInvalid
	}
	s, err := store.Open(databasePath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Integrity(ctx); err != nil {
		return "", err
	}
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(backupDir, 0700); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(backupDir, ".space-stage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := s.Backup(filepath.Join(stage, "space.db")); err != nil {
		return "", err
	}
	keyFile, err := os.OpenFile(filepath.Join(stage, "auth.key"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, err = keyFile.Write(key)
	if err == nil {
		err = keyFile.Sync()
	}
	closeErr := keyFile.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	latestKey, err := os.ReadFile(keyPath)
	if err != nil {
		return "", err
	}
	if !equalBytes(key, latestKey) {
		return "", errors.New("encryption key changed during backup")
	}
	copyDB, err := store.Open(filepath.Join(stage, "space.db"))
	if err != nil {
		return "", err
	}
	if err := copyDB.Integrity(ctx); err != nil {
		_ = copyDB.Close()
		return "", err
	}
	copyAuth, err := auth.New(copyDB, filepath.Join(stage, "auth.key"))
	if err != nil {
		_ = copyDB.Close()
		return "", err
	}
	if err := copyAuth.VerifyBackupKey(ctx); err != nil {
		_ = copyDB.Close()
		return "", err
	}
	if err := copyDB.Close(); err != nil {
		return "", err
	}
	for _, name := range []string{"space.db", "auth.key"} {
		f, err := os.Open(filepath.Join(stage, name))
		if err != nil {
			return "", err
		}
		err = f.Sync()
		_ = f.Close()
		if err != nil {
			return "", err
		}
	}
	if err := syncDir(stage); err != nil {
		return "", err
	}
	name := "space-" + time.Now().UTC().Format("2006-01-02-150405.000000000") + "-" + store.ID() + ".bundle"
	final := filepath.Join(backupDir, name)
	if err := os.Rename(stage, final); err != nil {
		return "", err
	}
	if err := syncDir(backupDir); err != nil {
		return final, err
	}
	if err := pruneBundles(backupDir, retain); err != nil {
		return final, err
	}
	return final, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func pruneBundles(dir string, retain int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type bundle struct{ name, day string }
	all := []bundle{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		match := bundleName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		if _, err := time.Parse("2006-01-02", match[1]); err != nil {
			continue
		}
		all = append(all, bundle{name: entry.Name(), day: match[1]})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].name > all[j].name })
	keptDays := map[string]bool{}
	for _, b := range all {
		if !keptDays[b.day] && len(keptDays) < retain {
			keptDays[b.day] = true
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, b.name)); err != nil {
			return fmt.Errorf("remove old backup: %w", err)
		}
	}
	return syncDir(dir)
}
