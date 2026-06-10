package configdiff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func Explain(opts Options) (Result, error) {
	if opts.Vendor == "" {
		opts.Vendor = "auto"
	}

	beforeBytes, err := os.ReadFile(opts.BeforePath)
	if err != nil {
		return Result{}, fmt.Errorf("read before config: %w", err)
	}
	afterBytes, err := os.ReadFile(opts.AfterPath)
	if err != nil {
		return Result{}, fmt.Errorf("read after config: %w", err)
	}

	parser, err := selectParser(opts.Vendor, string(beforeBytes)+"\n"+string(afterBytes))
	if err != nil {
		return Result{}, err
	}
	before := parser.Parse(string(beforeBytes), opts.Vendor)
	after := parser.Parse(string(afterBytes), opts.Vendor)
	analysis := analyze(before, after, opts.Vendor)

	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}
	jsonBytes, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal analysis: %w", err)
	}
	if err := os.WriteFile(filepath.Join(opts.OutDir, "diff-analysis.json"), append(jsonBytes, '\n'), 0o600); err != nil {
		return Result{}, fmt.Errorf("write diff-analysis.json: %w", err)
	}
	if err := writeMarkdownReports(opts.OutDir, analysis); err != nil {
		return Result{}, err
	}
	if err := writeHTMLReport(opts.OutDir, analysis); err != nil {
		return Result{}, err
	}

	return Result{OutDir: opts.OutDir, Analysis: analysis}, nil
}
