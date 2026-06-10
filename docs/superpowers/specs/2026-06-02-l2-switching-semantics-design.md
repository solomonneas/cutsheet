# Unit 1 - L2 Switching Semantics (Catalyst-focused)

Date: 2026-06-02
Status: Approved direction (Approach A), data model approved.

## Goal

Add deterministic understanding of Layer 2 switching constructs to the analyzer so
Cisco Catalyst (and any IOS/IOS-XE switch) config diffs surface the switching-specific
risks operators actually care about during change review: spanning-tree, EtherChannel,
VTP, trunk/native VLAN scope, switchport mode, and storm-control.

The Catalyst config text already parses correctly through the existing `cisco-ios`
parser (Catalyst runs IOS/IOS-XE). This unit adds a **semantic layer** on top of the
existing block model. No parser surgery.

## Shared Architecture Conventions (referenced by Units 2-5)

These conventions are established here and reused by the later vendor units.

- **Layering:** parser produces `configBlock`s -> analyzer diffs blocks and extracts
  structured facts + risk findings -> deterministic provider renders reports. New
  semantics are added in the analyzer/report layers, not by mutating the parser unless
  a genuinely new config dialect requires it.
- **Structured facts before reports:** every new risk must be backed by a structured
  fact in `Analysis` (machine-readable), and the reports render those facts.
- **Risk findings reuse `RiskFinding`:** new risks use the existing `RiskFinding` struct
  with a new `category` string. They flow through the existing `add()` dedup closure in
  `riskFindings()` and the existing report renderers automatically.
- **Schema:** additive fields only. The hand-rolled validator in `schema_test.go` checks
  required keys + types but not `additionalProperties`, so new optional arrays are safe.
  `schema_version` bumps from `"1.0"` to `"1.1"` (update the enum in the schema file).
- **Golden tests:** `golden_test.go` compares a `goldenSummary` (counts + risk titles).
  Any change that alters counts or risk titles for an existing fixture requires
  regenerating that fixture's golden JSON. New vendors add a new fixture + golden entry.
- **File conventions:** one focused responsibility per file. New analyzer concerns get
  their own `*.go` file (e.g. `switching.go`). Detectors are small predicate/extractor
  functions, lowercased, line-oriented, `strings.ToLower` normalized.

## Data Model (APPROVED)

One discriminated fact type, mirroring `CategoryChange` but richer (subject + before/after):

```go
type SwitchingChange struct {
    Category   string   `json:"category"`     // switchport_mode | trunk | native_vlan |
                                               // spanning_tree | etherchannel | vtp | storm_control
    Subject    string   `json:"subject"`      // interface name, "port-channel1", "global", or VTP domain
    ChangeType string   `json:"change_type"`  // added | removed | changed
    Before     string   `json:"before,omitempty"`
    After      string   `json:"after,omitempty"`
    Evidence   []string `json:"evidence"`
}
```

Added to `Analysis`:

```go
SwitchingChanges []SwitchingChange `json:"switching_changes"`
```

`TouchedVLANs` / `TouchedInterfaces` are unchanged; this is purely additive L2 semantics.

## Detection Logic (`internal/configdiff/switching.go`)

Detectors operate on each `BlockChange`'s before/after lines. A `switchingChanges(changes)`
function returns `[]SwitchingChange`; an `appendSwitchingFindings(add, change)` helper is
called inside the existing `riskFindings()` loop so switching risks share the dedup closure
and ID numbering.

Detectors (all case-insensitive, line-oriented):

| Category | Lines matched | Subject |
| --- | --- | --- |
| `switchport_mode` | `switchport mode access\|trunk\|dynamic ...` | interface |
| `trunk` | `switchport trunk allowed vlan ...` (incl. add/remove/none/all, or absence) | interface |
| `native_vlan` | `switchport trunk native vlan N` | interface |
| `spanning_tree` (per-port) | `spanning-tree portfast`, `... bpduguard enable`, `... guard root\|loop`, and `no ...` forms | interface |
| `spanning_tree` (global) | `spanning-tree mode ...`, `spanning-tree vlan N priority M`, `spanning-tree vlan N root primary\|secondary` | `global` |
| `etherchannel` | `channel-group N mode active\|passive\|on\|desirable\|auto` (membership/mode) | interface or `port-channelN` |
| `vtp` | `vtp mode ...`, `vtp domain ...`, `vtp version ...`, `vtp pruning` | `global` or domain |
| `storm_control` | `storm-control broadcast\|multicast\|unicast level ...` | interface |

`before`/`after` carry the relevant statement(s) for `changed` blocks.

## Risk Findings (`switchingRiskFindings` via `appendSwitchingFindings`)

| Severity | Category | Title | Trigger |
| --- | --- | --- | --- |
| high | `switching` | Switchport mode changed | access<->trunk flip (security/loop/topology) |
| medium | `switching` | Trunk carries all VLANs | trunk with `allowed vlan all`, no prune, or `allowed vlan` removed |
| medium | `switching` | Trunk native VLAN changed | native VLAN delta (VLAN-hopping concern) |
| high | `spanning_tree` | Spanning-tree mode changed | `spanning-tree mode` delta (network-wide reconvergence) |
| high | `spanning_tree` | Spanning-tree root or priority changed | `spanning-tree vlan ... root\|priority` delta (topology shift) |
| high | `spanning_tree` | BPDU protection reduced or PortFast on trunk | BPDU guard removed/disabled, or PortFast on a trunk/uplink (loop risk) |
| medium | `etherchannel` | EtherChannel membership or mode changed | `channel-group` membership/mode delta (mismatched mode -> err-disable/outage) |
| high | `vtp` | VTP mode or domain changed | mode -> server, or domain delta (can erase the VLAN DB network-wide) |
| low | `switching` | Storm-control reduced or removed | storm-control removed/loosened |

Each finding carries a concrete recommendation and the matched lines as evidence/details,
following the existing `add(severity, category, title, recommendation, evidence, details)`
pattern.

## Reports

- `touched-objects.md`: new `## Switching / L2` section rendering `SwitchingChanges` as a
  table (Category | Subject | Change | Before | After), via a new `writeSwitching` helper
  in `reports.go`, wired into `renderTouchedObjects`.
- `validation-plan.md`: add conditional lines when switching changes are present
  (e.g. "Verify spanning-tree topology / root bridge", "Confirm EtherChannel members bundle",
  "Confirm VTP domain and mode are as intended").
- Risk findings flow into `risk-analysis.md` / `stakeholder-brief.md` automatically.

## Testing

- Unit tests in `configdiff_test.go` for each detector and each risk trigger
  (table-driven, before/after line pairs).
- New golden fixture: `testdata/catalyst-before.cfg` / `testdata/catalyst-after.cfg`
  exercising switchport mode flip, trunk allowed-VLAN change, native VLAN change,
  spanning-tree (portfast/bpduguard + global mode/root), channel-group, VTP, storm-control.
- New golden entry `testdata/golden/catalyst-summary.json` and a case in `golden_test.go`.
  Extend `goldenSummary` with a `SwitchingChanges int` count field.
- Regenerate `testdata/golden/cisco-summary.json` (risk titles change if the existing
  cisco sample contains switchport/STP lines).
- `schema_test.go` passes against the bumped `1.1` schema including `switching_changes`.

## Out of Scope (YAGNI)

- Full STP topology emulation / root-bridge election math.
- MST instance-to-VLAN mapping analysis (flag the mode change only).
- Per-VLAN load-balancing analysis across port-channels.
- Non-IOS switching dialects (covered by their own vendor units).
