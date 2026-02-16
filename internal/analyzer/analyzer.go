package analyzer

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"verity/internal/npm"
	"verity/internal/semver"
)

type Options struct {
	LockPath        string
	PackagePath     string
	ChangelogCheck  bool
	ChangelogStrict bool
}

type Report struct {
	GeneratedAt string          `json:"generated_at"`
	Summary     Summary         `json:"summary"`
	Packages    []PackageReport `json:"packages"`
}

type Summary struct {
	Total      int `json:"total"`
	UpToDate   int `json:"up_to_date"`
	SafeUpdate int `json:"safe_update"`
	Blocked    int `json:"blocked"`
	Unknown    int `json:"unknown"`
}

type PackageReport struct {
	Path             string   `json:"path"`
	Name             string   `json:"name"`
	CurrentVersion   string   `json:"current_version"`
	LatestVersion    string   `json:"latest_version"`
	RequestedRange   string   `json:"requested_range,omitempty"`
	UpdateType       string   `json:"update_type"`
	Status           string   `json:"status"`
	SafeToUpdate     bool     `json:"safe_to_update"`
	RiskScore        int      `json:"risk_score"`
	BlockingReasons  []string `json:"blocking_reasons,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
	ChangelogChecked bool     `json:"changelog_checked"`
	ChangelogSources []string `json:"changelog_sources,omitempty"`
	ChangelogSignals []string `json:"changelog_signals,omitempty"`
	PeerChecksPassed int      `json:"peer_checks_passed"`
	PeerChecksFailed int      `json:"peer_checks_failed"`
}

func Run(ctx context.Context, opt Options) (*Report, error) {
	if opt.ChangelogStrict {
		opt.ChangelogCheck = true
	}

	if strings.TrimSpace(opt.LockPath) == "" && strings.TrimSpace(opt.PackagePath) == "" {
		return nil, fmt.Errorf("no input files provided: pass --lock and/or --package")
	}

	requested := map[string]string{}
	manifestLoaded := false
	if strings.TrimSpace(opt.PackagePath) != "" {
		manifest, manErr := npm.LoadManifest(opt.PackagePath)
		if manErr != nil {
			return nil, manErr
		}
		requested = manifest.RequestedRanges()
		manifestLoaded = true
	}

	installed := []npm.InstalledPackage{}
	if strings.TrimSpace(opt.LockPath) != "" {
		lockPkgs, lockErr := npm.LoadInstalledFromLock(opt.LockPath)
		if lockErr != nil {
			return nil, lockErr
		}
		installed = append(installed, lockPkgs...)
	}

	if len(installed) == 0 && manifestLoaded {
		// No lockfile input; fall back to direct dependencies from package.json.
		for name, constraint := range requested {
			installed = append(installed, npm.InstalledPackage{
				Path:    "direct/" + name,
				Name:    name,
				Version: strings.TrimPrefix(constraint, "^"),
			})
		}
	}

	if len(installed) == 0 {
		return nil, fmt.Errorf("no dependencies found in %s or %s", opt.LockPath, opt.PackagePath)
	}

	idx := npm.BuildInstalledIndex(installed)
	registry := npm.NewRegistryClient(20 * time.Second)

	out := make([]PackageReport, len(installed))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range installed {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = analyzeOne(ctx, registry, installed[i], idx, requested[installed[i].Name], opt.ChangelogCheck, opt.ChangelogStrict)
		}()
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Path < out[j].Path
		}
		return out[i].Name < out[j].Name
	})

	summary := Summary{Total: len(out)}
	for _, p := range out {
		switch p.Status {
		case "up-to-date":
			summary.UpToDate++
		case "safe-update":
			summary.SafeUpdate++
		case "blocked":
			summary.Blocked++
		default:
			summary.Unknown++
		}
	}

	return &Report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Summary:     summary,
		Packages:    out,
	}, nil
}

func analyzeOne(ctx context.Context, registry *npm.RegistryClient, p npm.InstalledPackage, installedIndex map[string]string, requestedRange string, changelogCheck bool, changelogStrict bool) PackageReport {
	r := PackageReport{
		Path:           p.Path,
		Name:           p.Name,
		CurrentVersion: p.Version,
		RequestedRange: requestedRange,
		UpdateType:     "unknown",
		Status:         "unknown",
		SafeToUpdate:   false,
	}

	latest, err := registry.LatestVersion(ctx, p.Name)
	if err != nil {
		r.Warnings = append(r.Warnings, err.Error())
		r.RiskScore = 60
		return r
	}
	r.LatestVersion = latest
	r.UpdateType = semver.DiffKind(p.Version, latest)

	if changelogCheck && r.UpdateType != "none" {
		r.ChangelogChecked = true
		signals, sources, err := registry.FeatureCompatibilitySignals(ctx, p.Name, p.Version, latest)
		if err != nil {
			r.Warnings = append(r.Warnings, "changelog check failed: "+err.Error())
		}
		if len(sources) > 0 {
			r.ChangelogSources = sources
		}
		for _, s := range signals {
			r.ChangelogSignals = append(r.ChangelogSignals, s.Reason)
			if s.Level == "high" || (changelogStrict && s.Level == "medium") {
				r.BlockingReasons = append(r.BlockingReasons, "changelog: "+s.Reason)
				continue
			}
			r.Warnings = append(r.Warnings, "changelog: "+s.Reason)
		}
	}

	peerDeps, found, err := registry.PeerDependencies(ctx, p.Name, latest)
	if err != nil {
		r.Warnings = append(r.Warnings, "peer dependency lookup failed: "+err.Error())
	} else if found {
		for peerName, constraint := range peerDeps {
			installedV, ok := installedIndex[peerName]
			if !ok {
				r.PeerChecksFailed++
				r.BlockingReasons = append(r.BlockingReasons, fmt.Sprintf("missing peer %s (%s)", peerName, constraint))
				continue
			}
			okSat, known := semver.Satisfies(installedV, constraint)
			if !known {
				r.Warnings = append(r.Warnings, fmt.Sprintf("unsupported peer range %s@%s", peerName, constraint))
				r.PeerChecksFailed++
				continue
			}
			if !okSat {
				r.PeerChecksFailed++
				r.BlockingReasons = append(r.BlockingReasons, fmt.Sprintf("peer conflict %s wants %s but installed %s", peerName, constraint, installedV))
				continue
			}
			r.PeerChecksPassed++
		}
	}

	if requestedRange != "" && r.UpdateType != "none" {
		ok, known := semver.Satisfies(latest, requestedRange)
		if known && !ok {
			r.BlockingReasons = append(r.BlockingReasons, "latest is outside package.json range")
		}
		if !known {
			r.Warnings = append(r.Warnings, "unsupported package.json range")
		}
	}

	r.RiskScore = scoreRisk(r)

	switch {
	case r.UpdateType == "none":
		r.Status = "up-to-date"
		r.SafeToUpdate = true
		r.RiskScore = 0
	case len(r.BlockingReasons) > 0:
		r.Status = "blocked"
		r.SafeToUpdate = false
	case len(r.Warnings) > 0 || r.UpdateType == "unknown":
		r.Status = "unknown"
		r.SafeToUpdate = false
	case r.UpdateType == "major":
		r.Status = "blocked"
		r.SafeToUpdate = false
		r.BlockingReasons = append(r.BlockingReasons, "major version bump")
	default:
		r.Status = "safe-update"
		r.SafeToUpdate = true
	}

	if r.RiskScore > 100 {
		r.RiskScore = 100
	}
	return r
}

func scoreRisk(r PackageReport) int {
	score := 0
	switch r.UpdateType {
	case "none":
		score += 0
	case "patch":
		score += 10
	case "minor":
		score += 25
	case "major":
		score += 45
	default:
		score += 30
	}

	score += r.PeerChecksFailed * 20
	score += len(r.BlockingReasons) * 10
	score += len(r.Warnings) * 8

	if r.UpdateType == "none" {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
