package semver

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string
}

func Parse(raw string) (Version, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "v")
	if raw == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	partsMeta := strings.SplitN(raw, "+", 2)
	partsPre := strings.SplitN(partsMeta[0], "-", 2)
	core := strings.Split(partsPre[0], ".")
	if len(core) != 3 {
		return Version{}, fmt.Errorf("not a concrete semver: %q", raw)
	}
	maj, err := strconv.Atoi(core[0])
	if err != nil {
		return Version{}, err
	}
	min, err := strconv.Atoi(core[1])
	if err != nil {
		return Version{}, err
	}
	pat, err := strconv.Atoi(core[2])
	if err != nil {
		return Version{}, err
	}
	v := Version{Major: maj, Minor: min, Patch: pat}
	if len(partsPre) == 2 {
		v.Pre = partsPre[1]
	}
	return v, nil
}

func Compare(a, b Version) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	if a.Pre == "" && b.Pre != "" {
		return 1
	}
	if a.Pre != "" && b.Pre == "" {
		return -1
	}
	return strings.Compare(a.Pre, b.Pre)
}

func DiffKind(current, latest string) string {
	cv, err := Parse(current)
	if err != nil {
		return "unknown"
	}
	lv, err := Parse(latest)
	if err != nil {
		return "unknown"
	}
	cmp := Compare(cv, lv)
	if cmp >= 0 {
		return "none"
	}
	if cv.Major != lv.Major {
		return "major"
	}
	if cv.Minor != lv.Minor {
		return "minor"
	}
	if cv.Patch != lv.Patch || cv.Pre != lv.Pre {
		return "patch"
	}
	return "none"
}

func Satisfies(version, expr string) (bool, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" {
		return true, true
	}
	orParts := strings.Split(expr, "||")
	knownAny := false
	for _, part := range orParts {
		ok, known := satisfiesAnd(version, strings.TrimSpace(strings.ReplaceAll(part, ",", " ")))
		if known {
			knownAny = true
		}
		if ok && known {
			return true, true
		}
	}
	if !knownAny {
		return false, false
	}
	return false, true
}

func satisfiesAnd(version, expr string) (bool, bool) {
	tokens := strings.Fields(expr)
	if len(tokens) == 0 {
		return true, true
	}
	for _, t := range tokens {
		ok, known := satisfiesToken(version, t)
		if !known {
			return false, false
		}
		if !ok {
			return false, true
		}
	}
	return true, true
}

func satisfiesToken(version, token string) (bool, bool) {
	if token == "*" || token == "x" || token == "X" {
		return true, true
	}
	v, err := Parse(version)
	if err != nil {
		return false, false
	}

	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(token, op) {
			want, err := Parse(strings.TrimSpace(strings.TrimPrefix(token, op)))
			if err != nil {
				return false, false
			}
			cmp := Compare(v, want)
			switch op {
			case ">=":
				return cmp >= 0, true
			case "<=":
				return cmp <= 0, true
			case ">":
				return cmp > 0, true
			case "<":
				return cmp < 0, true
			case "=":
				return cmp == 0, true
			}
		}
	}

	if strings.HasPrefix(token, "^") {
		base, err := Parse(strings.TrimPrefix(token, "^"))
		if err != nil {
			return false, false
		}
		upper := Version{Major: base.Major + 1}
		if base.Major == 0 {
			if base.Minor == 0 {
				upper = Version{Major: 0, Minor: 0, Patch: base.Patch + 1}
			} else {
				upper = Version{Major: 0, Minor: base.Minor + 1}
			}
		}
		return Compare(v, base) >= 0 && Compare(v, upper) < 0, true
	}

	if strings.HasPrefix(token, "~") {
		base, err := Parse(strings.TrimPrefix(token, "~"))
		if err != nil {
			return false, false
		}
		upper := Version{Major: base.Major, Minor: base.Minor + 1}
		return Compare(v, base) >= 0 && Compare(v, upper) < 0, true
	}

	if strings.Contains(token, "x") || strings.Contains(token, "X") || strings.Contains(token, "*") {
		parts := strings.Split(token, ".")
		if len(parts) == 3 {
			maj, err1 := strconv.Atoi(parts[0])
			minWildcard := parts[1] == "x" || parts[1] == "X" || parts[1] == "*"
			patWildcard := parts[2] == "x" || parts[2] == "X" || parts[2] == "*"
			if err1 != nil {
				return false, false
			}
			if minWildcard {
				return v.Major == maj, true
			}
			min, err2 := strconv.Atoi(parts[1])
			if err2 != nil {
				return false, false
			}
			if patWildcard {
				return v.Major == maj && v.Minor == min, true
			}
		}
		return false, false
	}

	if strings.Count(token, ".") == 2 {
		want, err := Parse(token)
		if err != nil {
			return false, false
		}
		return Compare(v, want) == 0, true
	}

	return false, false
}
