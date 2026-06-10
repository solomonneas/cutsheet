package snapshots

import (
	"errors"
	"testing"
)

func openTestStore(t *testing.T) *SnapshotStore {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestFirstSaveCommits(t *testing.T) {
	s := openTestStore(t)

	res, err := s.Save("gw1", []byte("hostname gw1\n"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !res.Changed {
		t.Fatal("first save: Changed = false, want true")
	}
	if res.CommitHash == "" {
		t.Fatal("first save: empty CommitHash")
	}
	if res.PrevCommitHash != "" {
		t.Fatalf("first save: PrevCommitHash = %q, want empty", res.PrevCommitHash)
	}
	if res.PrevContent != nil {
		t.Fatalf("first save: PrevContent = %q, want nil", res.PrevContent)
	}

	got, err := s.Get("gw1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hostname gw1\n" {
		t.Fatalf("Get: got %q", got)
	}
}

func TestIdenticalSaveIsNoOp(t *testing.T) {
	s := openTestStore(t)
	content := []byte("hostname gw1\n")

	first, err := s.Save("gw1", content)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second, err := s.Save("gw1", content)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if second.Changed {
		t.Fatal("identical save: Changed = true, want false")
	}
	if second.CommitHash != first.CommitHash {
		t.Fatalf("identical save: CommitHash = %q, want %q (no new commit)", second.CommitHash, first.CommitHash)
	}
}

func TestChangedSaveReturnsPrevContent(t *testing.T) {
	s := openTestStore(t)

	first, err := s.Save("gw1", []byte("hostname gw1\nsnmp-server community alpha\n"))
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second, err := s.Save("gw1", []byte("hostname gw1\n"))
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if !second.Changed {
		t.Fatal("changed save: Changed = false, want true")
	}
	if second.CommitHash == "" || second.CommitHash == first.CommitHash {
		t.Fatalf("changed save: CommitHash = %q (first %q)", second.CommitHash, first.CommitHash)
	}
	if second.PrevCommitHash != first.CommitHash {
		t.Fatalf("changed save: PrevCommitHash = %q, want %q", second.PrevCommitHash, first.CommitHash)
	}
	if string(second.PrevContent) != "hostname gw1\nsnmp-server community alpha\n" {
		t.Fatalf("changed save: PrevContent = %q", second.PrevContent)
	}

	// Historical content is retrievable by commit hash.
	old, err := s.GetAt("gw1", first.CommitHash)
	if err != nil {
		t.Fatalf("GetAt: %v", err)
	}
	if string(old) != "hostname gw1\nsnmp-server community alpha\n" {
		t.Fatalf("GetAt: got %q", old)
	}
	cur, err := s.Get("gw1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(cur) != "hostname gw1\n" {
		t.Fatalf("Get: got %q", cur)
	}
}

func TestMultipleDevicesDoNotInterfere(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.Save("gw1", []byte("hostname gw1\n")); err != nil {
		t.Fatalf("Save gw1: %v", err)
	}
	swRes, err := s.Save("sw1", []byte("hostname sw1\n"))
	if err != nil {
		t.Fatalf("Save sw1: %v", err)
	}
	if !swRes.Changed {
		t.Fatal("sw1 first save: Changed = false")
	}
	if swRes.PrevCommitHash != "" {
		t.Fatalf("sw1 first save: PrevCommitHash = %q, want empty (gw1 commits must not count)", swRes.PrevCommitHash)
	}

	// Changing gw1 must not affect sw1's prev tracking.
	gwRes, err := s.Save("gw1", []byte("hostname gw1-renamed\n"))
	if err != nil {
		t.Fatalf("Save gw1 change: %v", err)
	}
	if !gwRes.Changed {
		t.Fatal("gw1 change: Changed = false")
	}
	if string(gwRes.PrevContent) != "hostname gw1\n" {
		t.Fatalf("gw1 change: PrevContent = %q", gwRes.PrevContent)
	}

	sw, err := s.Get("sw1")
	if err != nil {
		t.Fatalf("Get sw1: %v", err)
	}
	if string(sw) != "hostname sw1\n" {
		t.Fatalf("Get sw1: got %q", sw)
	}
}

func TestReopenExistingRepo(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first, err := s1.Save("gw1", []byte("hostname gw1\n"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	res, err := s2.Save("gw1", []byte("hostname gw1\n"))
	if err != nil {
		t.Fatalf("Save after reopen: %v", err)
	}
	if res.Changed {
		t.Fatal("identical save after reopen: Changed = true, want false")
	}
	if res.CommitHash != first.CommitHash {
		t.Fatalf("after reopen: CommitHash = %q, want %q", res.CommitHash, first.CommitHash)
	}
}

func TestGetUnknownDevice(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.Get("ghost"); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("Get ghost: got %v, want ErrNoSnapshot", err)
	}
}

func TestInvalidDeviceID(t *testing.T) {
	s := openTestStore(t)
	for _, id := range []string{"", "../escape", "a/b", "a\\b", "."} {
		if _, err := s.Save(id, []byte("x")); err == nil {
			t.Errorf("Save(%q): want error, got nil", id)
		}
	}
}
