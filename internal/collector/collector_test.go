package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name          string
		collectorType string
		configJSON    string
		wantErr       bool
	}{
		{"file ok", "file", `{"path":"/etc/hostname"}`, false},
		{"file empty path", "file", `{"path":""}`, true},
		{"file missing path", "file", `{}`, true},
		{"file bad json", "file", `{`, true},
		{"unknown type", "carrier-pigeon", `{}`, true},
		{"empty type", "", `{}`, true},
		{"ssh not implemented yet", "ssh", `{"address":"198.18.0.1"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.collectorType, []byte(tt.configJSON))
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c == nil {
				t.Fatal("New returned nil collector")
			}
		})
	}
}

func TestFileCollectorFetch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.cfg")
	want := "hostname gw1\ninterface eth0\n address 198.18.0.1/24\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	c, err := New("file", []byte(`{"path":"`+path+`"}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != want {
		t.Fatalf("Fetch: got %q, want %q", got, want)
	}
}

func TestFileCollectorFetchMissingFile(t *testing.T) {
	c, err := New("file", []byte(`{"path":"`+filepath.Join(t.TempDir(), "absent.cfg")+`"}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch missing file: want error, got nil")
	}
}

func TestFileCollectorFetchCancelledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "running.cfg")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	c, err := New("file", []byte(`{"path":"`+path+`"}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Fetch(ctx); err == nil {
		t.Fatal("Fetch with cancelled context: want error, got nil")
	}
}
