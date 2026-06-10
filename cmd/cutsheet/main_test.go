package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solomonneas/cutsheet/internal/secrets"
)

func TestParseDeviceAdd(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
		check   func(t *testing.T, got addedDevice)
	}{
		{
			name: "full flags",
			args: []string{
				"--id", "edge-gw1", "--name", "Edge Gateway", "--vendor", "edgeos",
				"--address", "198.18.0.1", "--collector", "file",
				"--config", `{"path":"/var/lib/cutsheet/fixtures/gw1.cfg"}`,
				"--interval", "300",
			},
			check: func(t *testing.T, got addedDevice) {
				d := got.device
				if d.ID != "edge-gw1" || d.Name != "Edge Gateway" || d.Vendor != "edgeos" ||
					d.Address != "198.18.0.1" || d.CollectorType != "file" ||
					d.PollIntervalSeconds != 300 || !d.Enabled {
					t.Fatalf("device: %+v", d)
				}
			},
		},
		{
			name: "defaults",
			args: []string{"--id", "sw1", "--collector", "file", "--config", `{"path":"/tmp/sw1.cfg"}`},
			check: func(t *testing.T, got addedDevice) {
				d := got.device
				if d.Name != "sw1" {
					t.Fatalf("Name default: got %q, want id", d.Name)
				}
				if d.Vendor != "auto" {
					t.Fatalf("Vendor default: got %q, want auto", d.Vendor)
				}
				if d.PollIntervalSeconds != 300 {
					t.Fatalf("interval default: got %d, want 300", d.PollIntervalSeconds)
				}
				if !d.Enabled {
					t.Fatal("Enabled default: got false, want true")
				}
			},
		},
		{
			name:    "missing id",
			args:    []string{"--collector", "file", "--config", `{"path":"/tmp/x.cfg"}`},
			wantErr: "--id",
		},
		{
			name:    "bad id characters",
			args:    []string{"--id", "bad id!", "--collector", "file", "--config", `{"path":"/tmp/x.cfg"}`},
			wantErr: "device id",
		},
		{
			name:    "unknown collector",
			args:    []string{"--id", "x1", "--collector", "carrier-pigeon", "--config", `{}`},
			wantErr: "collector",
		},
		{
			name:    "invalid collector config",
			args:    []string{"--id", "x1", "--collector", "file", "--config", `{"path":""}`},
			wantErr: "collector",
		},
		{
			name:    "negative interval",
			args:    []string{"--id", "x1", "--collector", "file", "--config", `{"path":"/tmp/x.cfg"}`, "--interval", "-5"},
			wantErr: "interval",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDeviceAdd(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDeviceAdd: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestValidDeviceID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gw1", true},
		{"edge-gw1", true},
		{"core.sw_2", true},
		{"GW1", true},
		{"", false},
		{"-leading-dash", false},
		{"has space", false},
		{"slash/y", false},
		{"dots/../up", false},
	}
	for _, tt := range tests {
		if got := validDeviceID(tt.id); got != tt.want {
			t.Errorf("validDeviceID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestParseDeviceAddNetworkCollectors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
		check   func(t *testing.T, got addedDevice)
	}{
		{
			name: "unifi defaults vendor to unifi-json",
			args: []string{"--id", "ctrl1", "--collector", "unifi",
				"--config", `{"url":"https://ctrl.example.invalid","username":"audit","password":"pw"}`},
			check: func(t *testing.T, got addedDevice) {
				if got.device.Vendor != "unifi-json" {
					t.Fatalf("Vendor: got %q, want unifi-json", got.device.Vendor)
				}
			},
		},
		{
			name: "explicit vendor wins over unifi default",
			args: []string{"--id", "ctrl1", "--vendor", "auto", "--collector", "unifi",
				"--config", `{"url":"https://ctrl.example.invalid","username":"audit","password":"pw"}`},
			check: func(t *testing.T, got addedDevice) {
				if got.device.Vendor != "auto" {
					t.Fatalf("Vendor: got %q, want explicit auto", got.device.Vendor)
				}
			},
		},
		{
			name: "ssh preset junos defaults vendor",
			args: []string{"--id", "mx1", "--collector", "ssh",
				"--config", `{"host":"mx1.example.invalid","username":"audit","password":"pw","preset":"junos","insecure_ignore_host_key":true}`},
			check: func(t *testing.T, got addedDevice) {
				if got.device.Vendor != "junos" {
					t.Fatalf("Vendor: got %q, want junos", got.device.Vendor)
				}
			},
		},
		{
			name: "ssh without preset keeps auto vendor",
			args: []string{"--id", "gw1", "--collector", "ssh",
				"--config", `{"host":"gw1.example.invalid","username":"audit","password":"pw","command":"show config","insecure_ignore_host_key":true}`},
			check: func(t *testing.T, got addedDevice) {
				if got.device.Vendor != "auto" {
					t.Fatalf("Vendor: got %q, want auto", got.device.Vendor)
				}
			},
		},
		{
			name: "unifi missing url rejected",
			args: []string{"--id", "ctrl1", "--collector", "unifi",
				"--config", `{"username":"audit","password":"pw"}`},
			wantErr: "url",
		},
		{
			name: "ssh missing host key policy rejected",
			args: []string{"--id", "gw1", "--collector", "ssh",
				"--config", `{"host":"gw1.example.invalid","username":"audit","password":"pw","preset":"edgeos"}`},
			wantErr: "host_key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDeviceAdd(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDeviceAdd: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestRunDeviceAddEncryptsCredentials(t *testing.T) {
	t.Setenv(secrets.EnvKey, "")
	dataDir := t.TempDir()

	err := runDeviceAdd([]string{
		"--data-dir", dataDir, "--id", "gw1", "--collector", "ssh",
		"--config", `{"host":"gw1.example.invalid","username":"audit","password":"tape-and-string","preset":"edgeos","insecure_ignore_host_key":true}`,
	})
	if err != nil {
		t.Fatalf("runDeviceAdd: %v", err)
	}

	st, err := openStore(dataDir)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer st.Close()
	d, err := st.GetDevice(context.Background(), "gw1")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(d.CollectorConfig), &cfg); err != nil {
		t.Fatalf("parse stored config: %v", err)
	}
	stored, _ := cfg["password"].(string)
	if !secrets.IsEncrypted(stored) {
		t.Fatalf("stored password not encrypted: %q", stored)
	}
	if strings.Contains(d.CollectorConfig, "tape-and-string") {
		t.Fatal("plaintext password leaked into stored config")
	}

	box, err := secrets.Open(dataDir)
	if err != nil {
		t.Fatalf("secrets.Open: %v", err)
	}
	plain, err := box.Decrypt(stored)
	if err != nil {
		t.Fatalf("Decrypt stored password: %v", err)
	}
	if string(plain) != "tape-and-string" {
		t.Fatalf("decrypted password: got %q", plain)
	}
}

func TestRunDeviceAddFileSkipsSecretKey(t *testing.T) {
	t.Setenv(secrets.EnvKey, "")
	dataDir := t.TempDir()
	err := runDeviceAdd([]string{
		"--data-dir", dataDir, "--id", "fx1", "--collector", "file",
		"--config", `{"path":"/var/lib/cutsheet/fixtures/fx1.cfg"}`,
	})
	if err != nil {
		t.Fatalf("runDeviceAdd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "secret.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file collector should not generate a secret key (stat err: %v)", err)
	}
}
