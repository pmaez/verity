package output

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"verity/internal/analyzer"
)

func RenderTable(r *analyzer.Report) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 2, 2, 2, ' ', 0)

	fmt.Fprintf(w, "PACKAGE\tCURRENT\tLATEST\tUPDATE\tSTATUS\tRISK\tREASONS\n")
	for _, p := range r.Packages {
		reasons := "-"
		if len(p.BlockingReasons) > 0 {
			reasons = strings.Join(p.BlockingReasons, "; ")
		}
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			p.Name,
			p.CurrentVersion,
			emptyDash(p.LatestVersion),
			p.UpdateType,
			p.Status,
			p.RiskScore,
			reasons,
		)
	}
	w.Flush()

	fmt.Fprintf(&b, "\nSummary: total=%d up_to_date=%d safe_update=%d blocked=%d unknown=%d\n",
		r.Summary.Total,
		r.Summary.UpToDate,
		r.Summary.SafeUpdate,
		r.Summary.Blocked,
		r.Summary.Unknown,
	)
	return b.String()
}

func emptyDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
