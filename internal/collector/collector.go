// Package collector fetches device configurations. Collectors are read-only
// by design: Cutsheet never writes to a device. v1 ships the "file" collector
// (fixtures and demo mode); SSH and UniFi API collectors register here in a
// later phase.
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// Collector fetches the current configuration of one device.
type Collector interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// factory builds a collector from its JSON config.
type factory func(configJSON []byte) (Collector, error)

// factories maps collector type names to constructors. New collector types
// (ssh, unifi, ...) slot in here.
var factories = map[string]factory{
	"file": newFileCollector,
}

// New builds a collector of the given type from its JSON config.
func New(collectorType string, configJSON []byte) (Collector, error) {
	f, ok := factories[collectorType]
	if !ok {
		return nil, fmt.Errorf("unknown collector type %q", collectorType)
	}
	return f(configJSON)
}

// fileCollector reads a config from a local file. Used for fixture-driven
// tests and the zero-hardware demo mode.
type fileCollector struct {
	path string
}

func newFileCollector(configJSON []byte) (Collector, error) {
	var cfg struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, fmt.Errorf("parse file collector config: %w", err)
	}
	if cfg.Path == "" {
		return nil, fmt.Errorf("file collector config: %q is required", "path")
	}
	return &fileCollector{path: cfg.Path}, nil
}

func (c *fileCollector) Fetch(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return content, nil
}
