// Command cutsheet runs the Cutsheet server and manages the device registry.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"text/tabwriter"

	"github.com/solomonneas/cutsheet/internal/collector"
	"github.com/solomonneas/cutsheet/internal/scheduler"
	"github.com/solomonneas/cutsheet/internal/snapshots"
	"github.com/solomonneas/cutsheet/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "cutsheet: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "device":
		return runDevice(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		return usageError()
	}
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "data directory (database + snapshot repo)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return fmt.Errorf("serve requires --data-dir")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	st, snaps, err := openDataDir(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	// The analysis pipeline plugs in here later; for now changes are logged.
	handler := func(ctx context.Context, device store.Device, result snapshots.SaveResult) {
		logger.Info("config change detected",
			"device", device.ID,
			"commit", result.CommitHash,
			"prev_commit", result.PrevCommitHash)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sched := scheduler.New(st, snaps, handler, scheduler.Options{Logger: logger})
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}

	devices, err := st.ListDevices(ctx)
	if err != nil {
		return err
	}
	logger.Info("cutsheet server started", "data_dir", *dataDir, "devices", len(devices))

	<-ctx.Done()
	logger.Info("shutting down")
	sched.Stop()
	return nil
}

func runDevice(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "add":
		return runDeviceAdd(args[1:])
	case "list":
		return runDeviceList(args[1:])
	case "rm":
		return runDeviceRm(args[1:])
	default:
		return usageError()
	}
}

// addedDevice is the parsed result of `device add` flags.
type addedDevice struct {
	dataDir string
	device  store.Device
}

func parseDeviceAdd(args []string) (addedDevice, error) {
	fs := flag.NewFlagSet("device add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "data directory")
	id := fs.String("id", "", "device id (slug: letters, digits, . _ -)")
	name := fs.String("name", "", "display name (defaults to id)")
	vendor := fs.String("vendor", "auto", "configdiff parser mode (e.g. cisco-ios, unifi-json, auto)")
	address := fs.String("address", "", "device address")
	collectorType := fs.String("collector", "file", "collector type (v1: file)")
	configJSON := fs.String("config", "{}", "collector config JSON")
	interval := fs.Int("interval", 300, "poll interval in seconds (0 = manual only)")
	disabled := fs.Bool("disabled", false, "register the device without polling it")
	if err := fs.Parse(args); err != nil {
		return addedDevice{}, err
	}

	if *id == "" {
		return addedDevice{}, fmt.Errorf("device add requires --id")
	}
	if !validDeviceID(*id) {
		return addedDevice{}, fmt.Errorf("invalid device id %q: use letters, digits, . _ - (must start with a letter or digit)", *id)
	}
	if *interval < 0 {
		return addedDevice{}, fmt.Errorf("interval must be >= 0, got %d", *interval)
	}
	// Validate the collector type and config up front so a bad registration
	// fails at add time, not at first poll.
	if _, err := collector.New(*collectorType, []byte(*configJSON)); err != nil {
		return addedDevice{}, fmt.Errorf("invalid collector: %w", err)
	}
	if *name == "" {
		*name = *id
	}

	return addedDevice{
		dataDir: *dataDir,
		device: store.Device{
			ID:                  *id,
			Name:                *name,
			Vendor:              *vendor,
			Address:             *address,
			CollectorType:       *collectorType,
			CollectorConfig:     *configJSON,
			PollIntervalSeconds: *interval,
			Enabled:             !*disabled,
		},
	}, nil
}

var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validDeviceID(id string) bool {
	return deviceIDPattern.MatchString(id)
}

func runDeviceAdd(args []string) error {
	parsed, err := parseDeviceAdd(args)
	if err != nil {
		return err
	}
	if parsed.dataDir == "" {
		return fmt.Errorf("device add requires --data-dir")
	}
	st, err := openStore(parsed.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.CreateDevice(context.Background(), parsed.device); err != nil {
		return err
	}
	fmt.Printf("Added device %s\n", parsed.device.ID)
	return nil
}

func runDeviceList(args []string) error {
	fs := flag.NewFlagSet("device list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return fmt.Errorf("device list requires --data-dir")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	devices, err := st.ListDevices(context.Background())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tVENDOR\tCOLLECTOR\tINTERVAL\tENABLED")
	for _, d := range devices {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%t\n",
			d.ID, d.Name, d.Vendor, d.CollectorType, d.PollIntervalSeconds, d.Enabled)
	}
	return w.Flush()
}

func runDeviceRm(args []string) error {
	fs := flag.NewFlagSet("device rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "data directory")
	id := fs.String("id", "", "device id to remove")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *id == "" {
		return fmt.Errorf("device rm requires --data-dir and --id")
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.DeleteDevice(context.Background(), *id); err != nil {
		return err
	}
	fmt.Printf("Removed device %s\n", *id)
	return nil
}

func openStore(dataDir string) (*store.Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return store.Open(filepath.Join(dataDir, "cutsheet.db"))
}

func openDataDir(dataDir string) (*store.Store, *snapshots.SnapshotStore, error) {
	st, err := openStore(dataDir)
	if err != nil {
		return nil, nil, err
	}
	snaps, err := snapshots.Open(filepath.Join(dataDir, "snapshots"))
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return st, snaps, nil
}

func usageError() error {
	printUsage()
	return fmt.Errorf("expected command: serve or device add|list|rm")
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  cutsheet serve --data-dir ./data")
	fmt.Fprintln(os.Stderr, "  cutsheet device add --data-dir ./data --id edge-gw1 --name 'Edge Gateway' --vendor edgeos --collector file --config '{\"path\":\"/abs/path/gw1.cfg\"}' --interval 300")
	fmt.Fprintln(os.Stderr, "  cutsheet device list --data-dir ./data")
	fmt.Fprintln(os.Stderr, "  cutsheet device rm --data-dir ./data --id edge-gw1")
}
