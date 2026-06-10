# Implementation Notes

Running log of decisions, deviations, and tradeoffs not captured in the spec
(`docs/superpowers/specs/2026-06-09-cutsheet-design.md`). Newest entries at the bottom.

## 2026-06-09 - Bootstrap

- **Fresh history instead of imported history.** The spec allowed importing
  config-diff-explainer's git history. Chose to vendor the working tree at a single
  initial commit instead: the source repo's history contains tracked
  `.claude/memory-handoffs/` files, and scrubbing history before a future public
  release (filter-repo) has been painful on past projects. Cutsheet history is clean
  from commit zero. Provenance is recorded in the initial commit message.
- **Carried uncommitted WIP from the source repo.** config-diff-explainer had
  finished-looking uncommitted work on `feat/multi-vendor-expansion` (interactive
  HTML report sections, search/filter, escaping test). It builds and tests green, so
  it came along. The source repo was left untouched.
- **Layout:** `internal/configdiff` became `pkg/configdiff` (public library, the
  server will import it), `cmd/config-diff` became `cmd/cutsheet-cli`. Module path
  `github.com/solomonneas/cutsheet`. 30 tests passing after the move.
- **Fixture IP sanitization.** content-guard blocks all RFC 1918 even in fake
  fixtures. All `10.x` fixture/doc/test IPs were remapped into 198.18.0.0/15
  (RFC 2544 benchmark range) with a subnet-preserving mapping:
  `10.0.B.C -> 198.18.B.C`; other `10.A.B.C` prefixes got distinct `198.19.Y.C`
  third octets (10.10.0->198.19.10, 10.10.10->198.19.110, 10.20.0->198.19.20,
  10.20.20->198.19.120, 10.30.0->198.19.30, 10.50.0->198.19.50,
  10.255.0->198.19.255, 10.1.1->198.19.1, 10.9.0->198.19.9). Tests pass unchanged
  in behavior because the mapping is injective per /24.
- **`.claude/` is fully gitignored** in this repo (no tracked handoffs), unlike the
  source repo. Handoffs still get written to `.claude/memory-handoffs/` on disk for
  the local memory ingester; they just never enter git.
- **content-guard pre-push hook** installed from `~/repos/content-guard/hooks/pre-push`.
- **Dogfood constraint:** no enterprise lab hardware available (Cisco Catalyst /
  Palo Alto access ended). Collector order: UniFi API first, EdgeOS/VyOS second,
  Cisco IOS via containerlab/fixtures.
- **Name:** Trunkline was the first pick; burned (live MCP-infra product at
  trunkline.dev). Cutsheet cleared on GitHub/npm/PyPI/cutsheet.dev/.io as of
  2026-06-09. Domain registration is Solomon's action item.
