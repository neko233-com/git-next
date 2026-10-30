// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func ownedDirectory(path string) (os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	return info, errors.Join(err, file.Close())
}

// The caller creates this unique directory; never clean a replacement or a path
// that resolves outside the report's parent. Failed runs retain their fixtures.
func removeOwnedChild(parentPath, work string, owned os.FileInfo) error {
	if !filepath.IsAbs(parentPath) || !filepath.IsAbs(work) || filepath.Dir(work) != parentPath || owned == nil {
		return fmt.Errorf("refusing to remove an unexpected owned path")
	}
	linkInfo, err := os.Lstat(work)
	if err != nil || linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.IsDir() {
		return fmt.Errorf("fixture directory changed; retained")
	}
	info, err := ownedDirectory(work)
	if err != nil || !os.SameFile(owned, info) {
		return fmt.Errorf("fixture directory changed; retained")
	}
	parent, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(work)
	if err != nil || filepath.Dir(resolved) != parent {
		return fmt.Errorf("fixture directory escaped its report parent; retained")
	}
	if err = os.RemoveAll(work); err != nil {
		return fmt.Errorf("fixture cleanup failed: %w", err)
	}
	return nil
}

func finishBenchmarkReport(output, work string, owned os.FileInfo, report record, runErr error, keep bool) error {
	report["fixtures_retained"] = true
	if err := writeNewJSON(output, report); err != nil {
		return errors.Join(runErr, err)
	}
	if runErr != nil || keep {
		return runErr
	}
	if work != output+".fixtures" {
		return fmt.Errorf("refusing to remove an unexpected fixture path")
	}
	if err := removeOwnedChild(filepath.Dir(output), work, owned); err != nil {
		return err
	}
	report["fixtures_retained"] = false
	if err := writeJSON(output, report); err != nil {
		return fmt.Errorf("fixture cleanup completed but report update failed: %w", err)
	}
	return nil
}
