# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [0.3.0] - 2026-11-12

### Changed

- `unprotected-critical` ignores workloads whose `preemptionPolicy` is `Never`
  when picking the aggressor: they cannot evict anyone. A critical pod outranked
  only by such workloads gets the new low-severity `outranked-non-preempting`
  finding instead of a high one.
- The text ladder has a `P` column (`P` may preempt, `-` is `Never`), and JSON
  ladder entries carry `preempting`.

## [0.2.0]

### Security

- golang.org/x/net v0.38.0 -> v0.62.0 and golang.org/x/text v0.23.0 -> v0.43.0.
  govulncheck reported 8 reachable vulnerabilities (7 in x/net, including HTTP/2
  transport issues on the path the tool uses to talk to the API server, 1 in
  x/text); after the update it reports none.

### Changed

- k8s.io/api, k8s.io/apimachinery and k8s.io/client-go v0.34.1 -> v0.37.2.
- Minimum Go is now 1.26 (go directive was 1.27.1). The CI matrix runs Go 1.26 and
  1.27 with `GOTOOLCHAIN=local` and checks that each cell really uses its version;
  before, the matrix listed 1.22-1.24 but every cell auto-switched to go1.27.1.
- actions/checkout and actions/setup-go bumped to v7.
- Dependabot configured for GitHub Actions and Go modules (k8s.io modules grouped).

## [0.1.0]

- First release: priority ladder, per-node preemption order, and the
  `missing-priorityclass`, `no-global-default`, `multiple-global-defaults` and
  `unprotected-critical` rules.

[0.3.0]: https://github.com/moveeeax/k8s-priorityclass-audit/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/moveeeax/k8s-priorityclass-audit/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/moveeeax/k8s-priorityclass-audit/releases/tag/v0.1.0
