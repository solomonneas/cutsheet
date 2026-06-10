package main

import (
	"strings"
	"testing"
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
