package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"verity/internal/semver"
)

type RegistryClient struct {
	baseURL string
	http    *http.Client

	mu                sync.Mutex
	cache             map[string]*PackageDoc
	changelogCache    map[string][]CompatibilitySignal
	changelogSrcCache map[string][]string
	releasesCache     map[string][]githubRelease
}

type PackageDoc struct {
	DistTags   map[string]string                `json:"dist-tags"`
	Versions   map[string]PackageVersionDetails `json:"versions"`
	Repository repositoryInfo                   `json:"repository"`
	Homepage   string                           `json:"homepage"`
}

type PackageVersionDetails struct {
	PeerDependencies map[string]string `json:"peerDependencies"`
	Repository       repositoryInfo    `json:"repository"`
	Homepage         string            `json:"homepage"`
}

type repositoryInfo struct {
	URL string `json:"url"`
}

type CompatibilitySignal struct {
	Level  string
	Reason string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
}

func NewRegistryClient(timeout time.Duration) *RegistryClient {
	return &RegistryClient{
		baseURL: "https://registry.npmjs.org",
		http: &http.Client{
			Timeout: timeout,
		},
		cache:             map[string]*PackageDoc{},
		changelogCache:    map[string][]CompatibilitySignal{},
		changelogSrcCache: map[string][]string{},
		releasesCache:     map[string][]githubRelease{},
	}
}

func (c *RegistryClient) FetchPackageDoc(ctx context.Context, name string) (*PackageDoc, error) {
	c.mu.Lock()
	if doc, ok := c.cache[name]; ok {
		c.mu.Unlock()
		return doc, nil
	}
	c.mu.Unlock()

	u := c.baseURL + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", name, resp.StatusCode)
	}
	var doc PackageDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode package doc %s: %w", name, err)
	}

	c.mu.Lock()
	c.cache[name] = &doc
	c.mu.Unlock()
	return &doc, nil
}

func (c *RegistryClient) LatestVersion(ctx context.Context, name string) (string, error) {
	doc, err := c.FetchPackageDoc(ctx, name)
	if err != nil {
		return "", err
	}
	latest := doc.DistTags["latest"]
	if latest == "" {
		return "", fmt.Errorf("missing dist-tags.latest for %s", name)
	}
	return latest, nil
}

func (c *RegistryClient) PeerDependencies(ctx context.Context, name, version string) (map[string]string, bool, error) {
	doc, err := c.FetchPackageDoc(ctx, name)
	if err != nil {
		return nil, false, err
	}
	v, ok := doc.Versions[version]
	if !ok {
		return nil, false, nil
	}
	return v.PeerDependencies, true, nil
}

func (c *RegistryClient) FeatureCompatibilitySignals(ctx context.Context, name, current, latest string) ([]CompatibilitySignal, []string, error) {
	cacheKey := name + "|" + current + "->" + latest
	c.mu.Lock()
	if sigs, ok := c.changelogCache[cacheKey]; ok {
		srcs := append([]string{}, c.changelogSrcCache[cacheKey]...)
		c.mu.Unlock()
		return append([]CompatibilitySignal{}, sigs...), srcs, nil
	}
	c.mu.Unlock()

	doc, err := c.FetchPackageDoc(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	versionSet, err := versionsInUpgradeRange(doc.Versions, current, latest)
	if err != nil {
		return nil, nil, err
	}
	if len(versionSet) == 0 {
		return nil, nil, nil
	}

	repoURL := repositoryURL(doc, latest)
	owner, repo, ok := parseGitHubRepo(repoURL)
	if !ok {
		return nil, nil, fmt.Errorf("no GitHub repository found")
	}

	releases, err := c.fetchGitHubReleases(ctx, owner, repo)
	if err != nil {
		return nil, nil, err
	}

	signals := []CompatibilitySignal{}
	sources := []string{}
	for _, rel := range releases {
		if rel.Draft {
			continue
		}
		if !releaseMatchesVersion(rel, versionSet) {
			continue
		}
		for _, sig := range detectSignals(rel.Body) {
			signals = append(signals, sig)
		}
		if rel.HTMLURL != "" {
			sources = append(sources, rel.HTMLURL)
		}
	}

	signals = dedupeSignals(signals)
	sources = dedupeStrings(sources)

	c.mu.Lock()
	c.changelogCache[cacheKey] = append([]CompatibilitySignal{}, signals...)
	c.changelogSrcCache[cacheKey] = append([]string{}, sources...)
	c.mu.Unlock()
	return signals, sources, nil
}

func versionsInUpgradeRange(versionMap map[string]PackageVersionDetails, current, latest string) (map[string]struct{}, error) {
	currentV, err := semver.Parse(current)
	if err != nil {
		return nil, fmt.Errorf("current version is not concrete semver (%s)", current)
	}
	latestV, err := semver.Parse(latest)
	if err != nil {
		return nil, fmt.Errorf("latest version is not concrete semver (%s)", latest)
	}
	if semver.Compare(currentV, latestV) >= 0 {
		return map[string]struct{}{}, nil
	}

	versions := map[string]struct{}{}
	for raw := range versionMap {
		v, err := semver.Parse(raw)
		if err != nil {
			continue
		}
		if semver.Compare(v, currentV) > 0 && semver.Compare(v, latestV) <= 0 {
			versions[raw] = struct{}{}
			versions["v"+raw] = struct{}{}
		}
	}
	return versions, nil
}

func repositoryURL(doc *PackageDoc, latest string) string {
	if v, ok := doc.Versions[latest]; ok {
		if strings.TrimSpace(v.Repository.URL) != "" {
			return strings.TrimSpace(v.Repository.URL)
		}
		if strings.TrimSpace(v.Homepage) != "" {
			return strings.TrimSpace(v.Homepage)
		}
	}
	if strings.TrimSpace(doc.Repository.URL) != "" {
		return strings.TrimSpace(doc.Repository.URL)
	}
	return strings.TrimSpace(doc.Homepage)
}

func parseGitHubRepo(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	raw = strings.TrimPrefix(raw, "git+")
	raw = strings.TrimPrefix(raw, "git://")
	raw = strings.TrimSuffix(raw, ".git")
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if strings.HasPrefix(raw, "github:") {
		raw = strings.TrimPrefix(raw, "github:")
		parts := strings.Split(raw, "/")
		if len(parts) < 2 {
			return "", "", false
		}
		return parts[0], parts[1], true
	}
	if !strings.HasPrefix(raw, "github.com/") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "github.com/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (c *RegistryClient) fetchGitHubReleases(ctx context.Context, owner, repo string) ([]githubRelease, error) {
	key := owner + "/" + repo
	c.mu.Lock()
	if cached, ok := c.releasesCache[key]; ok {
		out := append([]githubRelease{}, cached...)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	u := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release notes: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release notes: status %d", resp.StatusCode)
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode release notes: %w", err)
	}

	c.mu.Lock()
	c.releasesCache[key] = append([]githubRelease{}, releases...)
	c.mu.Unlock()
	return releases, nil
}

func releaseMatchesVersion(rel githubRelease, versions map[string]struct{}) bool {
	for v := range versions {
		if releaseMentionsVersion(rel.TagName, v) || releaseMentionsVersion(rel.Name, v) {
			return true
		}
	}
	return false
}

func releaseMentionsVersion(raw, version string) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	version = strings.TrimSpace(strings.ToLower(version))
	if raw == "" || version == "" {
		return false
	}
	if raw == version {
		return true
	}
	needle := regexp.MustCompile(`(^|[^0-9])` + regexp.QuoteMeta(strings.TrimPrefix(version, "v")) + `([^0-9]|$)`)
	return needle.MatchString(raw)
}

func detectSignals(text string) []CompatibilitySignal {
	l := strings.ToLower(text)
	if strings.TrimSpace(l) == "" {
		return nil
	}

	out := []CompatibilitySignal{}
	high := map[string]string{
		"breaking":          "release notes mention breaking changes",
		"breaking change":   "release notes mention breaking changes",
		"drop support":      "release notes mention dropped platform/runtime support",
		"removed":           "release notes mention removed functionality",
		"migration guide":   "release notes require migration guidance",
		"incompatible":      "release notes mention incompatibility",
		"api change":        "release notes mention API changes",
		"backward-incompat": "release notes mention backward incompatibility",
	}
	for kw, reason := range high {
		if strings.Contains(l, kw) {
			out = append(out, CompatibilitySignal{Level: "high", Reason: reason})
		}
	}

	medium := map[string]string{
		"deprecated":      "release notes mention deprecations",
		"default changed": "release notes mention changed defaults",
		"behavior change": "release notes mention behavior changes",
		"renamed":         "release notes mention renamed APIs/options",
	}
	for kw, reason := range medium {
		if strings.Contains(l, kw) {
			out = append(out, CompatibilitySignal{Level: "medium", Reason: reason})
		}
	}
	return out
}

func dedupeSignals(in []CompatibilitySignal) []CompatibilitySignal {
	seen := map[string]struct{}{}
	out := make([]CompatibilitySignal, 0, len(in))
	for _, s := range in {
		k := s.Level + "|" + s.Reason
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Level == out[j].Level {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Level < out[j].Level
	})
	return out
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
