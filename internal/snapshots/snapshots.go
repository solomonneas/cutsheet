// Package snapshots stores device configuration snapshots in a server-managed
// git repository. Each device owns one canonical file
// (devices/<deviceID>/running-config) and every accepted change is a commit,
// which gives Cutsheet history, blame, and an offsite backup path for free.
package snapshots

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrNoSnapshot is returned when no snapshot exists for the device.
var ErrNoSnapshot = errors.New("no snapshot for device")

const (
	authorName  = "cutsheet"
	authorEmail = "noreply@cutsheet.local"
)

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// SaveResult describes the outcome of a Save call.
type SaveResult struct {
	// Changed is false when the content was identical to the latest snapshot
	// and no commit was made.
	Changed bool
	// CommitHash is the commit containing this content: the new commit when
	// Changed, otherwise the existing latest commit for the device.
	CommitHash string
	// PrevCommitHash is the previous commit that touched this device's file.
	// Empty on the first snapshot.
	PrevCommitHash string
	// PrevContent is the previous snapshot's content. Nil on the first
	// snapshot.
	PrevContent []byte
}

// SnapshotStore is a git-backed store of device config snapshots. It is safe
// for concurrent use.
type SnapshotStore struct {
	mu   sync.Mutex
	dir  string
	repo *git.Repository
}

// Open opens the snapshot repository at dir, initializing a plain repository
// with a filesystem worktree if none exists.
func Open(dir string) (*SnapshotStore, error) {
	repo, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return nil, fmt.Errorf("create snapshot dir: %w", mkErr)
		}
		repo, err = git.PlainInit(dir, false)
	}
	if err != nil {
		return nil, fmt.Errorf("open snapshot repo %s: %w", dir, err)
	}
	return &SnapshotStore{dir: dir, repo: repo}, nil
}

// Save records content as the latest snapshot for deviceID. Identical content
// is a no-op (Changed=false, no commit); new or changed content is committed.
func (s *SnapshotStore) Save(deviceID string, content []byte) (SaveResult, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return SaveResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := devicePath(deviceID)
	prev, prevExists, err := s.headFileContent(path)
	if err != nil {
		return SaveResult{}, fmt.Errorf("read previous snapshot for %s: %w", deviceID, err)
	}
	if prevExists && bytes.Equal(prev, content) {
		lastHash, err := s.lastCommitFor(path)
		if err != nil {
			return SaveResult{}, fmt.Errorf("resolve latest commit for %s: %w", deviceID, err)
		}
		return SaveResult{Changed: false, CommitHash: lastHash}, nil
	}

	prevHash := ""
	if prevExists {
		prevHash, err = s.lastCommitFor(path)
		if err != nil {
			return SaveResult{}, fmt.Errorf("resolve previous commit for %s: %w", deviceID, err)
		}
	}

	fullPath := filepath.Join(s.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return SaveResult{}, fmt.Errorf("create device dir: %w", err)
	}
	if err := os.WriteFile(fullPath, content, 0o600); err != nil {
		return SaveResult{}, fmt.Errorf("write snapshot file: %w", err)
	}

	wt, err := s.repo.Worktree()
	if err != nil {
		return SaveResult{}, fmt.Errorf("open worktree: %w", err)
	}
	if _, err := wt.Add(path); err != nil {
		return SaveResult{}, fmt.Errorf("stage snapshot: %w", err)
	}
	hash, err := wt.Commit("snapshot: "+deviceID, &git.CommitOptions{
		Author: &object.Signature{Name: authorName, Email: authorEmail, When: time.Now()},
	})
	if err != nil {
		return SaveResult{}, fmt.Errorf("commit snapshot for %s: %w", deviceID, err)
	}

	var prevContent []byte
	if prevExists {
		prevContent = prev
	}
	return SaveResult{
		Changed:        true,
		CommitHash:     hash.String(),
		PrevCommitHash: prevHash,
		PrevContent:    prevContent,
	}, nil
}

// Get returns the current (HEAD) snapshot content for deviceID.
func (s *SnapshotStore) Get(deviceID string) ([]byte, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	content, exists, err := s.headFileContent(devicePath(deviceID))
	if err != nil {
		return nil, fmt.Errorf("read snapshot for %s: %w", deviceID, err)
	}
	if !exists {
		return nil, fmt.Errorf("device %s: %w", deviceID, ErrNoSnapshot)
	}
	return content, nil
}

// GetAt returns the snapshot content for deviceID as of the given commit.
func (s *SnapshotStore) GetAt(deviceID, commitHash string) ([]byte, error) {
	if err := validateDeviceID(deviceID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	commit, err := s.repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return nil, fmt.Errorf("resolve commit %s: %w", commitHash, err)
	}
	content, exists, err := fileContent(commit, devicePath(deviceID))
	if err != nil {
		return nil, fmt.Errorf("read snapshot for %s at %s: %w", deviceID, commitHash, err)
	}
	if !exists {
		return nil, fmt.Errorf("device %s at %s: %w", deviceID, commitHash, ErrNoSnapshot)
	}
	return content, nil
}

func devicePath(deviceID string) string {
	return "devices/" + deviceID + "/running-config"
}

func validateDeviceID(deviceID string) error {
	if !deviceIDPattern.MatchString(deviceID) {
		return fmt.Errorf("invalid device id %q", deviceID)
	}
	return nil
}

// headFileContent returns the content of path in the HEAD commit, with exists
// reporting whether the file (or HEAD itself) is present.
func (s *SnapshotStore) headFileContent(path string) ([]byte, bool, error) {
	head, err := s.repo.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, false, nil // empty repository
	}
	if err != nil {
		return nil, false, err
	}
	commit, err := s.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, false, err
	}
	return fileContent(commit, path)
}

func fileContent(commit *object.Commit, path string) ([]byte, bool, error) {
	file, err := commit.File(path)
	if errors.Is(err, object.ErrFileNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	reader, err := file.Reader()
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// lastCommitFor returns the hash of the most recent commit that touched path,
// or "" if no commit has.
func (s *SnapshotStore) lastCommitFor(path string) (string, error) {
	iter, err := s.repo.Log(&git.LogOptions{FileName: &path})
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", nil // empty repository
	}
	if err != nil {
		return "", err
	}
	defer iter.Close()
	commit, err := iter.Next()
	if errors.Is(err, io.EOF) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return commit.Hash.String(), nil
}
