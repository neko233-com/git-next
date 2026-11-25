package gitx

import (
	"bytes"
	"crypto/sha1"
	"os"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage"
)

// tolerantStorage normalizes index files whose trailer checksum is all zeroes.
// Some Windows Git builds write that trailer when the index is not locked; the
// bytes are otherwise a valid v2/v3/v4 index. The on-disk file is left intact,
// so read-only commands never modify repository metadata.
type tolerantStorage struct {
	storage.Storer
	indexPath string
}

func (s *tolerantStorage) Index() (*index.Index, error) {
	data, err := os.ReadFile(s.indexPath)
	if os.IsNotExist(err) {
		return &index.Index{Version: 2}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) >= 20 && allZero(data[len(data)-20:]) {
		fixed := append([]byte(nil), data...)
		sum := sha1.Sum(data[:len(data)-20])
		copy(fixed[len(fixed)-20:], sum[:])
		idx := &index.Index{}
		if err := index.NewDecoder(bytes.NewReader(fixed)).Decode(idx); err == nil {
			return idx, nil
		}
	}
	return s.Storer.Index()
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

func openWithTolerantIndex(repo *git.Repository, gitDir string) (*git.Repository, error) {
	store, ok := repo.Storer.(storage.Storer)
	if !ok {
		return repo, nil
	}
	var worktree billy.Filesystem
	if wt, err := repo.Worktree(); err == nil {
		worktree = wt.Filesystem
	}
	wrapped := &tolerantStorage{Storer: store, indexPath: filepath.Join(gitDir, "index")}
	return git.Open(wrapped, worktree)
}
