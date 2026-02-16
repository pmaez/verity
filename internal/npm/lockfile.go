package npm

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type InstalledPackage struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type LockFile struct {
	LockfileVersion int                       `json:"lockfileVersion"`
	Name            string                    `json:"name"`
	Version         string                    `json:"version"`
	Packages        map[string]LockPkgEntry   `json:"packages"`
	Dependencies    map[string]LegacyLockNode `json:"dependencies"`
}

type LockPkgEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type LegacyLockNode struct {
	Version      string                    `json:"version"`
	Dependencies map[string]LegacyLockNode `json:"dependencies"`
}

func LoadInstalledFromLock(path string) ([]InstalledPackage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read package-lock.json: %w", err)
	}

	var lf LockFile
	if err := json.Unmarshal(b, &lf); err != nil {
		return nil, fmt.Errorf("parse package-lock.json: %w", err)
	}

	if len(lf.Packages) > 0 {
		pkgs := make([]InstalledPackage, 0, len(lf.Packages))
		for p, entry := range lf.Packages {
			if p == "" || entry.Version == "" {
				continue
			}
			name := entry.Name
			if name == "" {
				name = inferNameFromPath(p)
			}
			if name == "" {
				continue
			}
			pkgs = append(pkgs, InstalledPackage{Path: p, Name: name, Version: entry.Version})
		}
		sort.Slice(pkgs, func(i, j int) bool {
			if pkgs[i].Name == pkgs[j].Name {
				return pkgs[i].Path < pkgs[j].Path
			}
			return pkgs[i].Name < pkgs[j].Name
		})
		return pkgs, nil
	}

	pkgs := []InstalledPackage{}
	var walk func(prefix string, deps map[string]LegacyLockNode)
	walk = func(prefix string, deps map[string]LegacyLockNode) {
		for name, node := range deps {
			p := strings.Trim(prefix+"/node_modules/"+name, "/")
			pkgs = append(pkgs, InstalledPackage{Path: p, Name: name, Version: node.Version})
			if len(node.Dependencies) > 0 {
				walk(p, node.Dependencies)
			}
		}
	}
	walk("", lf.Dependencies)
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Name == pkgs[j].Name {
			return pkgs[i].Path < pkgs[j].Path
		}
		return pkgs[i].Name < pkgs[j].Name
	})
	return pkgs, nil
}

func inferNameFromPath(p string) string {
	idx := strings.LastIndex(p, "node_modules/")
	if idx == -1 {
		return ""
	}
	name := p[idx+len("node_modules/"):]
	if name == "" {
		return ""
	}
	parts := strings.Split(name, "/")
	if len(parts) >= 2 && strings.HasPrefix(parts[0], "@") {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func BuildInstalledIndex(pkgs []InstalledPackage) map[string]string {
	// Prefer the package instance nearest to the root (shortest path).
	index := map[string]string{}
	depth := map[string]int{}
	for _, p := range pkgs {
		d := strings.Count(p.Path, "node_modules/")
		if curDepth, ok := depth[p.Name]; !ok || d < curDepth {
			depth[p.Name] = d
			index[p.Name] = p.Version
		}
	}
	return index
}
