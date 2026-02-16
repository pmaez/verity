package output

import (
	"fmt"
	"sort"
	"strings"

	"verity/internal/analyzer"
)

type treeNode struct {
	report analyzer.PackageReport
}

func RenderTree(r *analyzer.Report) string {
	nodes := map[string]treeNode{}
	children := map[string][]string{}
	roots := []string{}

	for _, p := range r.Packages {
		nodes[p.Path] = treeNode{report: p}
	}

	for path := range nodes {
		parent := parentPath(path)
		if parent == "" || nodes[parent].report.Path == "" {
			roots = append(roots, path)
			continue
		}
		children[parent] = append(children[parent], path)
	}

	sortPaths(roots, nodes)
	for parent := range children {
		sortPaths(children[parent], nodes)
	}

	var b strings.Builder
	b.WriteString("Dependency Tree\n")
	for i, path := range roots {
		last := i == len(roots)-1
		renderNode(&b, path, "", last, nodes, children)
	}
	fmt.Fprintf(&b, "\nSummary: total=%d up_to_date=%d safe_update=%d blocked=%d unknown=%d\n",
		r.Summary.Total,
		r.Summary.UpToDate,
		r.Summary.SafeUpdate,
		r.Summary.Blocked,
		r.Summary.Unknown,
	)
	return b.String()
}

func renderNode(b *strings.Builder, path, prefix string, isLast bool, nodes map[string]treeNode, children map[string][]string) {
	n := nodes[path]
	branch := "├── "
	nextPrefix := prefix + "│   "
	if isLast {
		branch = "└── "
		nextPrefix = prefix + "    "
	}

	fmt.Fprintf(
		b,
		"%s%s%s@%s -> %s [%s risk=%d]\n",
		prefix,
		branch,
		n.report.Name,
		n.report.CurrentVersion,
		emptyDash(n.report.LatestVersion),
		n.report.Status,
		n.report.RiskScore,
	)

	kids := children[path]
	for i, kid := range kids {
		renderNode(b, kid, nextPrefix, i == len(kids)-1, nodes, children)
	}
}

func parentPath(path string) string {
	if strings.HasPrefix(path, "direct/") {
		return ""
	}
	if strings.HasPrefix(path, "node_modules/") && !strings.Contains(path[len("node_modules/"):], "/node_modules/") {
		return ""
	}
	idx := strings.LastIndex(path, "/node_modules/")
	if idx == -1 {
		return ""
	}
	return path[:idx]
}

func sortPaths(paths []string, nodes map[string]treeNode) {
	sort.Slice(paths, func(i, j int) bool {
		ni := nodes[paths[i]].report.Name
		nj := nodes[paths[j]].report.Name
		if ni == nj {
			return paths[i] < paths[j]
		}
		return ni < nj
	})
}
