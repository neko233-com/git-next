// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGameResourceMultiAssetPathsAndSingleAssetCompatibility(t *testing.T) {
	for engine, original := range map[string]string{"unity": "Assets/Art/Large.asset", "unreal": "Content/Large.uasset"} {
		paths, e := gameResourceAssetPaths(engine, 1)
		if e != nil || len(paths) != 1 || paths[0] != original {
			t.Fatal("default one-asset fixture path changed", engine, paths, e)
		}
		paths, e = gameResourceAssetPaths(engine, 16)
		if e != nil || len(paths) != 16 {
			t.Fatal("maximum multiasset fixture rejected", engine, e)
		}
		seen := map[string]bool{}
		for _, path := range paths {
			if seen[path] || strings.Contains(path, "..") || filepath.IsAbs(path) || !strings.HasSuffix(path, filepath.Ext(original)) || filepath.ToSlash(filepath.Dir(path)) != filepath.ToSlash(filepath.Dir(original)) {
				t.Fatal("multiasset path is duplicate, unsafe or outside the engine's tracked directory", path)
			}
			seen[path] = true
		}
	}
	for _, count := range []int{-1, 0, 17} {
		if _, e := gameResourceAssetPaths("unity", count); e == nil {
			t.Fatal("unsafe asset count accepted", count)
		}
	}
	if _, e := gameResourceAssetPaths("unknown", 1); e == nil {
		t.Fatal("unknown engine accepted")
	}
	if !strings.Contains(" "+commands["benchmark-game-resources"].options+" ", " asset-files ") {
		t.Fatal("asset-files option is unreachable from the CLI")
	}
}

func TestGameResourceMultiAssetSeedsAndBothRevisionsCoverEveryAsset(t *testing.T) {
	seen := map[string]bool{}
	for ordinal, engine := range []string{"unity", "unity", "unreal", "unreal"} {
		root := t.TempDir()
		paths, e := gameResourceAssetPaths(engine, 3)
		if e != nil {
			t.Fatal(e)
		}
		assets, e := gameResourceGenerateAssets(root, paths, 32768, ordinal)
		if e != nil || len(assets) != len(paths) {
			t.Fatal("not every asset was generated", e)
		}
		old := map[string][]byte{}
		for _, asset := range assets {
			data, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(asset.path)))
			if e != nil || int64(len(data)) != asset.old.bytes || digestBytes(data) != asset.old.sha || seen[asset.old.sha] {
				t.Fatal("asset seeds are not independent or their complete bytes were not retained", asset.path, e)
			}
			old[asset.path], seen[asset.old.sha] = data, true
		}
		if e = gameResourceReviseAssets(root, assets, ordinal); e != nil {
			t.Fatal(e)
		}
		for i, asset := range assets {
			data, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(asset.path)))
			before, offset := old[asset.path], int(asset.old.bytes/2)
			if e != nil || len(data) != len(before) || digestBytes(data) == asset.old.sha || !bytes.Equal(data[:offset], before[:offset]) || !bytes.Equal(data[offset+4096:], before[offset+4096:]) || !bytes.Equal(data[offset:offset+4096], bytes.Repeat([]byte{byte(ordinal + 1 + 4*i)}, 4096)) {
				t.Fatal("second revision did not change exactly the selected 4 KiB region of every asset", asset.path, e)
			}
		}
	}
	root := t.TempDir()
	paths, _ := gameResourceAssetPaths("unity", 1)
	assets, e := gameResourceGenerateAssets(root, paths, 32768, 0)
	if e != nil {
		t.Fatal(e)
	}
	legacy := filepath.Join(t.TempDir(), "original-seed")
	if e = gameResourceStreamAsset(legacy, 32768, 233); e != nil {
		t.Fatal(e)
	}
	identity, e := gameResourceFileDigest(legacy)
	if e != nil || identity != assets[0].old {
		t.Fatal("default asset's original bytes changed", e)
	}
}

func gameResourceMultiAssetHistoryTestFixture(t *testing.T) (gameResourceFixture, []gameResourceResult) {
	t.Helper()
	f := gameResourceFixture{name: "generated-project", files: map[string]gameResourceFile{}}
	rows := []gameResourceResult{}
	paths, e := gameResourceAssetPaths("unity", 3)
	if e != nil {
		t.Fatal(e)
	}
	for i, path := range paths {
		old := bytes.Repeat([]byte{byte(i + 1)}, 32+i)
		current := bytes.Repeat([]byte{byte(i + 11)}, 32+i)
		f.assets = append(f.assets, gameResourceAsset{path: path, old: gameResourceFile{int64(len(old)), digestBytes(old)}})
		f.files[path] = gameResourceFile{int64(len(current)), digestBytes(current)}
		for _, data := range [][]byte{current, old} {
			rows = append(rows, gameResourceResult{Status: "pass", ExitCode: 0, StdoutBytes: int64(len(data)), StdoutSHA256: digestBytes(data)})
		}
	}
	return f, rows
}

func TestGameResourceMultiAssetHistoryRejectsAnyMissingOrDamagedRevision(t *testing.T) {
	f, rows := gameResourceMultiAssetHistoryTestFixture(t)
	if problems, verified := gameResourceValidateAssetHistory(f, rows); len(problems) != 0 || verified != 6 {
		t.Fatal("all three complete asset revisions should pass", problems, verified)
	}
	for row := range rows {
		for _, mutation := range []string{"sha", "bytes", "exit", "deadline"} {
			copyRows := append([]gameResourceResult{}, rows...)
			switch mutation {
			case "sha":
				copyRows[row].StdoutSHA256 = digestBytes([]byte("corrupt later asset revision"))
			case "bytes":
				copyRows[row].StdoutBytes--
			case "exit":
				copyRows[row].ExitCode = 1
			case "deadline":
				copyRows[row].TimedOut = true
			}
			if problems, verified := gameResourceValidateAssetHistory(f, copyRows); len(problems) == 0 || verified == 6 {
				t.Fatal("a damaged current/historical revision was ignored", row, mutation)
			}
		}
	}
	if problems, _ := gameResourceValidateAssetHistory(f, rows[:2]); len(problems) == 0 {
		t.Fatal("checking only the first asset was accepted")
	}
	f.assets[2] = f.assets[0]
	if problems, _ := gameResourceValidateAssetHistory(f, rows); len(problems) == 0 {
		t.Fatal("a duplicate asset plan concealed an unvalidated file")
	}
	f, rows = gameResourceMultiAssetHistoryTestFixture(t)
	delete(f.files, f.assets[2].path)
	if problems, _ := gameResourceValidateAssetHistory(f, rows); len(problems) == 0 {
		t.Fatal("missing later worktree member accepted")
	}
}
