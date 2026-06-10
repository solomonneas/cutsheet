package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/solomonneas/cutsheet/pkg/configdiff"
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
	case "explain":
		fs := flag.NewFlagSet("explain", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		before := fs.String("before", "", "path to the before config")
		after := fs.String("after", "", "path to the after config")
		vendor := fs.String("vendor", "auto", "vendor parser mode: auto or generic")
		out := fs.String("out", "", "output report directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *before == "" || *after == "" || *out == "" {
			return fmt.Errorf("explain requires --before, --after, and --out")
		}

		result, err := configdiff.Explain(configdiff.Options{
			BeforePath: *before,
			AfterPath:  *after,
			Vendor:     *vendor,
			OutDir:     *out,
		})
		if err != nil {
			return err
		}
		fmt.Printf("Wrote config diff report to %s\n", result.OutDir)
		return nil
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		return usageError()
	}
}

func usageError() error {
	printUsage()
	return fmt.Errorf("expected command: explain")
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  cutsheet explain --before ./before.cfg --after ./after.cfg --vendor auto --out ./reports/change-001")
}
