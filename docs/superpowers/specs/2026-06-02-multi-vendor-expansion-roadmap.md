# Multi-Vendor Expansion - Roadmap and Cross-Cutting Decisions

Date: 2026-06-02

This expansion adds Catalyst L2 switching semantics plus four new vendor/format
parsers to `config-diff-explainer`. It is decomposed into five independently
shippable units, each with its own design spec in this directory.

## Units and sequencing

| # | Unit | Spec | Depends on |
| --- | --- | --- | --- |
| 1 | L2 switching semantics (Catalyst) | `2026-06-02-l2-switching-semantics-design.md` | none |
| 2 | EdgeSwitch / UbiquitiOS CLI | `2026-06-02-edgeswitch-cli-design.md` | Unit 1 (inherits L2 risks) |
| 3 | UniFi EdgeOS / VyOS set-style | `2026-06-02-unifi-edgeos-design.md` | none (reuses junos path) |
| 4 | Palo Alto PAN-OS (set-style) | `2026-06-02-panos-design.md` | none (reuses junos/fortinet patterns) |
| 5 | UniFi controller JSON | `2026-06-02-unifi-controller-json-design.md` | Unit 1 (reuses L2 detectors) |

Build order: 1 -> 2 -> 3 -> 4 -> 5. Unit 5 (JSON) is the largest and last.

## Cross-cutting decision 1: canonical `selectParser` auto ordering

Each unit spec proposed an ordering for its own detector. The merged canonical
order for the `auto` branch (most specific / unmistakable first, set-style
disambiguated by exclusive path heads, IOS-family before generic):

```
1. looksUnifiJSON   // JSON is unmistakable (starts with { or [, UniFi keys)
2. looksFortinet    // config/edit blocks
3. looksPANOS       // set rulebase / deviceconfig / mgt-config (PAN-OS-exclusive heads)
4. looksEdgeSwitch  // before cisco-ios: shares IOS tokens, needs priority
5. looksCiscoIOS
6. looksEdgeOS      // before junos: set-style, EdgeOS-exclusive heads
7. looksJunos
8. genericParser    // fallback
```

Rationale: set-style detectors (`looksPANOS`, `looksEdgeOS`) must key ONLY on
vendor-exclusive path heads and run before the more general set-style detector
(`looksJunos`) so a Junos config scores 0 in them and falls through. IOS-family
detectors (`looksEdgeSwitch`, `looksCiscoIOS`) share tokens, so the more specific
EdgeSwitch detector runs first; misclassification between them is byte-safe because
the parse is identical and only the vendor label differs.

Each unit, when implemented, inserts its detector at the position above rather than
at the location its own spec drafted in isolation.

## Cross-cutting decision 2: single `schema_version` bump to `1.1`

Unit 1 bumps `schema_version` `"1.0"` -> `"1.1"` and adds `switching_changes`.
Units 2, 3, and 4/5 add no NEW required schema fields beyond what Unit 1 introduced
(they reuse existing fact types; Unit 4 reuses `switching_changes`). The version
stays `"1.1"` across the whole expansion - later units do NOT bump it again. If a
later unit lands before Unit 1, it is responsible for performing the `1.1` bump and
adding `switching_changes` itself so the schema and code stay consistent. Given the
1 -> 5 build order, Unit 1 owns the bump.

## Cross-cutting decision 3: golden fixtures are generated, never hand-authored

Every new vendor fixture's golden summary JSON is produced from real `Explain`
output (run the binary / a regen helper), not written by hand, to avoid drift from
the actual analyzer behavior. Units that change existing analyzer output (Unit 1
changes the cisco sample's risk titles) must regenerate the affected existing
goldens in the same commit.

## Implementation status (all units complete)

All five units are implemented, tested, and committed on branch
`feat/multi-vendor-expansion`. Each unit ships green (`go build`/`go vet`/`go test`).

| # | Unit | Status | Parser label / detected vendor |
| --- | --- | --- | --- |
| 1 | L2 switching semantics | done | `cisco-ios` / `cisco` (Catalyst) |
| 2 | EdgeSwitch / UbiquitiOS CLI | done | `cisco-ios` / `ubiquiti` |
| 3 | UniFi EdgeOS / VyOS set-style | done | `edgeos` / `ubiquiti` |
| 4 | Palo Alto PAN-OS set-style | done | `panos` / `paloalto` |
| 5 | UniFi controller JSON | done | `unifi-json` / `ubiquiti` |

### Cross-cutting discovery: set-style detector collisions

The biggest implementation surprise: FortiOS, EdgeOS, and PAN-OS all emit `set service ...`
lines, so the weak `set service ` token over-triggered `looksFortinet` (EdgeOS misdetected as
FortiOS) and would have over-triggered `looksPANOS`. The fix, applied to both detectors:
require at least one vendor-**exclusive structural token** (FortiOS `config <section>`;
PAN-OS `set rulebase`/`deviceconfig`/`mgt-config`/`zone`/`virtual-router`) before a config can
be classified, so shared weak tokens cannot carry detection on their own. Final auto-detect
order as implemented: `unifiJSON -> fortinet -> panos -> edgeswitch -> cisco -> edgeos -> junos`.

### Vendor-neutral analyzer improvements made along the way

- "Route removed" risk now fires on any removed `route`-kind block (not only Cisco-style
  `ip route` lines), so PAN-OS/Fortinet/UniFi removed routes are flagged.
- `touchedRoutes` route-prefix fallback chains Fortinet then PAN-OS extractors.
- Firewall broadening and management-exposure detection gained EdgeOS-spaced and PAN-OS
  application-name spellings.

## Verification gate per unit

Each unit must, before its commit:
- `go build ./...`
- `go vet ./...`
- `go test ./...` (all green, including the new and regenerated goldens and the schema test)
- README updated (vendor list + Current Limitations)
