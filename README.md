# Verity

Verity is a Go CLI that inspects a Node project's dependencies and reports which packages are likely safe to update, which are blocked by compatibility constraints, and a numeric risk score per package.

## Current implementation

- Inputs:
  - `package-lock.json` only
  - `package.json` only
  - both together
- Dependency source:
  - With `package-lock.json`: analyzes recursive installed dependencies (supports lockfile `packages` map and legacy nested `dependencies`).
  - With `package.json` only: analyzes direct dependencies only (no recursive installed graph).
- Registry data:
  - Fetches `dist-tags.latest` from npm registry for each package.
  - Fetches `peerDependencies` for the latest version.
- Compatibility checks:
  - Compares installed vs latest version (`none`, `patch`, `minor`, `major`, `unknown`).
  - Evaluates latest package `peerDependencies` against installed versions.
  - If `package.json` is provided, checks whether latest is within requested dependency range.
  - Optional `--changelog-check` scans GitHub release notes for versions between current and latest and flags likely feature-level incompatibilities.
- Classification:
  - `up-to-date`, `safe-update`, `blocked`, `unknown`
- Risk score:
  - `0-100` based on update size, peer conflicts/blockers, and warnings/unknowns.

## Output

- `table` (default): human-readable terminal table.
- `tree`: ASCII dependency tree with status/risk annotations.
- `json`: CI-friendly structured report.

## Usage

```bash
go run ./cmd/verity \
  --lock /path/to/project/package-lock.json \
  --package /path/to/project/package.json \
  --changelog-check \
  --changelog-strict
```

JSON for CI:

```bash
go run ./cmd/verity --lock /path/to/project/package-lock.json --format json
```

Tree output:

```bash
go run ./cmd/verity --lock /path/to/project/package-lock.json --format tree
```

Run from another repo directory (where your `package.json` lives):

```bash
cd /path/to/your/node-project
go -C /Users/pmaez/dev/GoLang/verity run ./cmd/verity --package ./package.json
```

If both files exist:

```bash
cd /path/to/your/node-project
go -C /Users/pmaez/dev/GoLang/verity run ./cmd/verity --lock ./package-lock.json --package ./package.json
```

Build once and run from anywhere:

```bash
cd /Users/pmaez/dev/GoLang/verity
GOCACHE=/Users/pmaez/dev/GoLang/verity/.gocache go build -o verity ./cmd/verity

cd /path/to/your/node-project
/Users/pmaez/dev/GoLang/verity/verity --lock ./package-lock.json --package ./package.json
```

## CLI flags

- `--lock` path to `package-lock.json` (optional)
- `--package` path to `package.json` (optional)
- `--format` `table|tree|json` (default `table`)
- `--timeout` overall analysis timeout (default `45s`)
- `--changelog-check` include release-note/changelog heuristics (GitHub repos only)
- `--changelog-strict` treat medium changelog signals as blocking (implies `--changelog-check`)

## Notes

- You can pass only `--lock`, only `--package`, or both.
- If only `--package` is passed, Verity falls back to direct dependencies from `package.json` (no recursive lock graph available).
- If neither `--lock` nor `--package` is passed, Verity auto-detects `package-lock.json` and `package.json` in the current working directory.
- Semver range support is intentionally pragmatic (common operators: `^`, `~`, `>=`, `<=`, `<`, `>`, exact, wildcards). Complex expressions may be flagged as unknown and increase risk.
- Changelog checks are heuristic and keyword-based; they can produce false positives/negatives.
- Changelog checks currently rely on GitHub release notes discovered from package repository/homepage metadata.
- In strict mode, medium-severity changelog signals (for example deprecations/behavior changes) are treated as blockers instead of warnings.
