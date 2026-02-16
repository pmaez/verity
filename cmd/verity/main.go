package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"verity/internal/analyzer"
	"verity/internal/output"
)

func main() {
	var lockPath string
	var pkgPath string
	var format string
	var timeout time.Duration
	var changelogCheck bool
	var changelogStrict bool

	flag.StringVar(&lockPath, "lock", "", "path to package-lock.json (optional)")
	flag.StringVar(&pkgPath, "package", "", "path to package.json (optional)")
	flag.StringVar(&format, "format", "table", "output format: table|tree|json")
	flag.DurationVar(&timeout, "timeout", 45*time.Second, "analysis timeout")
	flag.BoolVar(&changelogCheck, "changelog-check", false, "analyze GitHub release notes/changelogs for possible feature-level incompatibilities")
	flag.BoolVar(&changelogStrict, "changelog-strict", false, "treat medium changelog signals as blocking (implies --changelog-check)")
	flag.Parse()

	if changelogStrict {
		changelogCheck = true
	}

	if lockPath == "" && pkgPath == "" {
		if _, err := os.Stat("package-lock.json"); err == nil {
			lockPath = filepath.Clean("package-lock.json")
		}
		if _, err := os.Stat("package.json"); err == nil {
			pkgPath = filepath.Clean("package.json")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	report, err := analyzer.Run(ctx, analyzer.Options{
		LockPath:        lockPath,
		PackagePath:     pkgPath,
		ChangelogCheck:  changelogCheck,
		ChangelogStrict: changelogStrict,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch strings.ToLower(format) {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding json: %v\n", err)
			os.Exit(1)
		}
	case "table":
		fmt.Print(output.RenderTable(report))
	case "tree":
		fmt.Print(output.RenderTree(report))
	default:
		fmt.Fprintf(os.Stderr, "error: unsupported format %q (use table|tree|json)\n", format)
		os.Exit(1)
	}
}
