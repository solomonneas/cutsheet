# Unit 2 - Ubiquiti EdgeSwitch / UbiquitiOS CLI Support

Date: 2026-06-02
Status: Approved direction (ride the cisco-ios parser path via a thin labeling wrapper).
Depends on: Unit 1 (`2026-06-02-l2-switching-semantics-design.md`) for the "Shared
Architecture Conventions" and the L2 switching semantics this unit inherits for free.

## Goal

Add first-class recognition of Ubiquiti EdgeSwitch / UbiquitiOS CLI configurations so
that before/after diffs of EdgeSwitch devices are labeled as `ubiquiti` and surface the
same switching change review (switchport, STP, EtherChannel/LAG, VLAN, trunk/native) that
Catalyst configs get, without standing up a second parser.

EdgeSwitch CLI is syntactically very close to Cisco IOS: it uses `interface`,
`vlan database`, `spanning-tree`, `switchport`, `line vty`, `ip route`, and broadly the
same indented-block structure. The decided direction is to **ride the existing
`cisco-ios` parser path** and add only a thin labeling wrapper plus an auto-detector. This
is the cheapest unit in the 5-unit expansion: no new block model, no new data model, no
new risk engine. It piggybacks on Unit 1's `cisco-ios` block parse and L2 semantics.

This is the cheapest unit. Scope is deliberately tight (YAGNI).

## Approach / Parser

Per the Shared Architecture Conventions in Unit 1: the parser layer is only touched when a
genuinely new config dialect requires it. EdgeSwitch does **not** require a new dialect; it
parses correctly through `parseGeneric` and the existing `classifyLine`. So this unit adds
a thin wrapper parser that reuses the cisco-ios parse and only overrides the labeling.

Introduce `edgeSwitchParser` in `internal/configdiff/parser.go`, mirroring how
`ciscoIOSParser` wraps `parseGeneric`:

```go
type edgeSwitchParser struct{}

func (edgeSwitchParser) Parse(text string, requestedVendor string) parsedConfig {
    parsed := parseGeneric(text, requestedVendor)
    parsed.Detection.Parser = "cisco-ios"          // traceable: same parse path as IOS
    parsed.Detection.DetectedVendor = "ubiquiti"   // clean vendor labeling
    if strings.EqualFold(requestedVendor, "auto") {
        parsed.Detection.Confidence = 0.72
    } else {
        parsed.Detection.Confidence = 0.86
    }
    parsed.Detection.Signals = appendSignal(parsed.Detection.Signals, "ubiquiti edgeswitch ios-style syntax")
    return parsed
}
```

Rationale for the thin wrapper over "just alias to ciscoIOSParser":
- Keeps `parser` field traceable to the actual parse implementation (`cisco-ios`), which is
  the truth: the block model and `classifyLine` path are identical.
- Gives clean `detected_vendor: "ubiquiti"` labeling so reports and the golden summary do
  not mislabel an EdgeSwitch device as Cisco.
- Adds a distinct signal string so a reviewer can see why the vendor was chosen.

Register the explicit vendor-mode aliases in `selectParser`:

```go
case "ubiquiti", "edgeswitch", "ubiquiti-edgeswitch", "ubiquitios", "edgeswitch-cli":
    return edgeSwitchParser{}, nil
```

Update the `default:` error string in `selectParser` to list the new modes alongside the
existing supported modes.

Rollback: `rollbackAnalysis` is driven by `analysis.DetectedPlatform.Parser`. Because the
wrapper sets `Parser = "cisco-ios"`, EdgeSwitch rollback automatically uses
`ciscoRollbackCommands` (IOS-style negation / reapply), which is correct for EdgeSwitch
CLI. No rollback work is needed in this unit.

## Detection

Add a `looksEdgeSwitch(text string) bool` predicate in `parser.go`, keyed on
EdgeSwitch-distinctive tokens that do **not** appear in mainstream Catalyst IOS configs.
Case-insensitive, line-oriented, `strings.ToLower` normalized, scoring like the existing
`looksCiscoIOS` / `looksFortinet` / `looksJunos` helpers.

Distinctive EdgeSwitch / UbiquitiOS tokens (strong signals, weight 2):

- `vlan database` (EdgeSwitch declares VLANs inside a `vlan database` block; classic IOS
  uses top-level `vlan N`)
- `serviceport ...` (EdgeSwitch out-of-band service port; not an IOS keyword)
- `network mgmt_vlan ...` and `network protocol ...` (EdgeSwitch management-network block)
- `vlan participation ...` / `vlan pvid ...` / `vlan tagging ...` (EdgeSwitch per-interface
  VLAN model instead of `switchport access/trunk vlan`)
- EdgeSwitch prompt/banner artifacts such as `(UBNT)` or an `EdgeSwitch`/`UBNT` hostname
  banner line if present in the capture

Weaker signals (weight 1), shared with IOS so only corroborating:

- `no spanning-tree` (EdgeSwitch commonly disables STP globally with this exact form)
- `set igmp` / `set igmp querier` (EdgeSwitch IGMP snooping form)

Threshold: `score >= 2` on the strong tokens (one strong distinctive token is enough,
because these tokens are effectively unique to EdgeSwitch).

### Ordering in `selectParser`'s `auto` branch (important)

EdgeSwitch overlaps heavily with Cisco IOS, so `looksCiscoIOS` will frequently also return
true for an EdgeSwitch config (it has `interface ...`, `switchport ...`, `line vty`). To
avoid an EdgeSwitch device being labeled `cisco`, `looksEdgeSwitch` MUST run **before**
`looksCiscoIOS` in the auto chain:

```go
case "auto":
    if looksFortinet(text) {
        return fortinetParser{}, nil
    }
    if looksEdgeSwitch(text) {     // before cisco-ios: shares IOS tokens, needs priority
        return edgeSwitchParser{}, nil
    }
    if looksCiscoIOS(text) {
        return ciscoIOSParser{}, nil
    }
    if looksJunos(text) {
        return junosParser{}, nil
    }
    return genericParser{}, nil
```

### Misclassification risk and acceptable fallback

- If `looksEdgeSwitch` fires on a config that is actually Catalyst IOS (false positive),
  the only consequence is the `detected_vendor` label flips to `ubiquiti`. The parse,
  block model, switching semantics, risk findings, and rollback are **identical** because
  both ride the `cisco-ios` path. No analysis correctness is lost, only the vendor label.
- If `looksEdgeSwitch` does not fire on a genuine EdgeSwitch config (false negative,
  e.g. a sparse diff that omits the distinctive `vlan database` / `serviceport` lines),
  it falls through to `looksCiscoIOS` and is labeled `cisco`. Again the parse is identical,
  so this is an acceptable graceful degradation, not a parse failure.
- Because the strong tokens are EdgeSwitch-unique, the strong-token threshold keeps false
  positives low while accepting that ambiguous captures degrade safely to `cisco-ios`
  labeling. This trade-off is intentional and documented; do not invest in a precise
  EdgeSwitch-vs-IOS classifier.

Explicit `--vendor ubiquiti` (or any alias) bypasses detection entirely and forces the
wrapper, which is the deterministic path operators should use when they know the device.

## Data Model Impact

None. No new struct, no new `Analysis` field, no schema change beyond what Unit 1 already
introduces. `detected_vendor` is an existing free-form string in `DetectedPlatform`, so
emitting `"ubiquiti"` requires no schema edit (the schema does not enum-constrain
`detected_vendor`).

Schema version: this unit does not bump `schema_version`. It inherits whatever Unit 1
lands (Unit 1 bumps `"1.0"` -> `"1.1"` for `switching_changes`). If Units 1 and 2 land
independently, Unit 2 stays at the current `"1.0"` and the EdgeSwitch golden simply omits
switching fields. Do not introduce a schema change from this unit.

## Risk Findings (inherited, no new code)

EdgeSwitch gets all switching/L2 risk findings **for free** because it rides the
`cisco-ios` block model and the analyzer's risk engine is parser-agnostic (it keys off
block `Kind` and line content, not vendor). Specifically EdgeSwitch inherits, with zero
new code in this unit:

- Unit 1 switching semantics: switchport mode flip, trunk-carries-all-VLANs, native VLAN
  change, spanning-tree mode/root/priority, BPDU protection, EtherChannel/LAG
  membership-or-mode, VTP, storm-control. These fire on the same `switchport ...`,
  `spanning-tree ...`, `channel-group ...` lines EdgeSwitch emits.
- Existing v1 risks already wired in `riskFindings()`: VLAN removed, interface VLAN
  assignment changed, trunk allowed VLAN list changed, interface shutdown state changed,
  default route changed, route removed, management/AAA/observability changes.

No `RiskFinding` category, no `add()` call, and no detector is added by this unit. If a
future EdgeSwitch-only keyword needs its own finding (e.g. `serviceport` management
exposure), that is a separate follow-up, out of scope here.

## EdgeSwitch syntax that genuinely differs from IOS

These differences are noted so reviewers understand how the cisco-ios classifier handles
or mis-handles them. None of them block this unit; the worst case is a line lands in a
`generic` block instead of a typed one, which still diffs correctly line-for-line.

| EdgeSwitch construct | IOS equivalent | How `classifyLine` handles it |
| --- | --- | --- |
| `vlan database` ... `vlan 10` (indented) | top-level `vlan 10` | `vlan database` is a multiline block header via the generic path; indented `vlan N` lines attach as block body lines. Diffs correctly; VLAN IDs still extracted by `vlanIDs` from the body lines. |
| `vlan participation include 10` / `vlan pvid 10` / `vlan tagging 10` (per-interface) | `switchport access vlan 10` / `switchport trunk ...` | Attach as interface block body lines (indented under `interface`). `interfaceVLANLine` matches `switchport ...` forms but NOT `vlan participation/pvid/tagging`, so the EdgeSwitch-native VLAN forms still diff as raw line changes but may NOT trigger the "interface VLAN assignment changed" risk. Documented gap; acceptable for this unit. |
| `serviceport ip ...` / `serviceport protocol ...` | OOB mgmt interface | Falls to `generic` line classification (no IOS keyword match). Diffs line-for-line; no typed extraction. Acceptable. |
| `network mgmt_vlan N` / `network protocol ...` | management VLAN/SVI config | `generic` line classification. Diffs correctly; not surfaced as a typed management change. Acceptable. |
| `no spanning-tree` (global STP disable) | `no spanning-tree vlan ...` style | Classified as a `generic` top-level line and diffs correctly. Unit 1's global spanning-tree detector keys on `spanning-tree mode` / `spanning-tree vlan`, so a bare `no spanning-tree` may NOT raise the STP risk. Documented gap; acceptable. |
| `set igmp` / `set igmp querier` | `ip igmp snooping ...` | Starts with `set`, which `detectPlatform` treats as a "set-style syntax" signal. This is cosmetic only (a stray signal string); it does not reroute the parser because the wrapper hard-sets `Parser`/`DetectedVendor`. Note it so the signal list on an EdgeSwitch report is understood. |

The key takeaway: EdgeSwitch's IOS-shaped lines (`interface`, `switchport`,
`spanning-tree mode`, `channel-group`, `line vty`, `ip route`) get full typed extraction
and inherited risks. EdgeSwitch-native forms (`vlan participation`, `serviceport`,
`network mgmt_vlan`, bare `no spanning-tree`) degrade to correct line-level diffing without
typed semantics. That is the intended scope.

## Reports

No report changes in this unit. All existing renderers (`change-summary.md`,
`risk-analysis.md`, `touched-objects.md`, `rollback-plan.md`, `validation-plan.md`,
`operator-checklist.md`, `stakeholder-brief.md`) render from the deterministic facts and
are parser/vendor-agnostic. The `detected_platform` block in the reports will simply show
`detected_vendor: ubiquiti`, `parser: cisco-ios`, and the EdgeSwitch signal string.

If Unit 1's `writeSwitching` / `validation-plan` switching additions land, EdgeSwitch
benefits from them automatically with no extra wiring.

## Testing

Follow the golden-test conventions from Unit 1.

- New golden fixture pair under `testdata/`:
  - `testdata/edgeswitch-before.cfg`
  - `testdata/edgeswitch-after.cfg`

  The fixtures should exercise both the IOS-shaped paths (so inherited risks fire) and at
  least one EdgeSwitch-native construct (so the parse is proven to degrade gracefully):
  a `vlan database` block; an interface with `switchport mode` flip and/or
  `switchport access vlan` change; a `spanning-tree mode` or `channel-group` change; a
  `line vty` / management change; plus EdgeSwitch-distinctive lines (`serviceport ...`,
  `network mgmt_vlan ...`, `vlan participation ...`, `no spanning-tree`) so `looksEdgeSwitch`
  fires and the native forms are present in the diff.

- New golden summary `testdata/golden/edgeswitch-summary.json`. Generate it from the actual
  `Explain` output (do not hand-author counts). It MUST assert
  `"detected_vendor": "ubiquiti"` and `"parser": "cisco-ios"` to lock in the labeling
  contract, plus the block/interface/vlan counts and risk titles produced by the fixtures.

- New case in `golden_test.go`'s `TestGoldenSummaries` table:
  ```go
  {
      name:       "edgeswitch",
      beforePath: filepath.Join("..", "..", "testdata", "edgeswitch-before.cfg"),
      afterPath:  filepath.Join("..", "..", "testdata", "edgeswitch-after.cfg"),
      goldenPath: filepath.Join("..", "..", "testdata", "golden", "edgeswitch-summary.json"),
  },
  ```
  Run the case with `Vendor: "auto"` so the test also proves `looksEdgeSwitch` ordering
  selects the wrapper (and the golden's `detected_vendor: ubiquiti` proves it did not fall
  through to `cisco`).

- Detection unit tests in `configdiff_test.go` (or the existing test file): table-driven
  cases asserting `looksEdgeSwitch` returns true on EdgeSwitch-distinctive snippets and
  false on a plain Catalyst IOS snippet, and that `selectParser("auto", edgeSwitchText)`
  returns a parser whose output has `DetectedVendor == "ubiquiti"`. Also assert
  `selectParser("ubiquiti", ...)` and the other aliases resolve to the wrapper.

- `schema_test.go`: no change needed for this unit. EdgeSwitch output is the same shape as
  cisco-ios output, which the schema already validates. (If Unit 1's `1.1` schema has
  landed, the EdgeSwitch output validates against it unchanged.)

- Existing `cisco-summary.json` / `junos-summary.json` goldens are unaffected by this unit
  (no shared code path changes their counts or labels). Confirm by running `make test`.

## Out of Scope (YAGNI)

- A standalone EdgeSwitch parser or any EdgeSwitch-specific block model. Riding cisco-ios
  is the decided direction; do not re-litigate.
- Typed extraction of EdgeSwitch-native VLAN syntax (`vlan participation`, `vlan pvid`,
  `vlan tagging`) into `TouchedVLAN`/switching semantics. Lines still diff; typed
  understanding of these forms is a later enhancement.
- EdgeSwitch-specific risk findings (e.g. `serviceport` management exposure, bare
  `no spanning-tree` global STP disable). Inherited IOS-shaped risks only.
- EdgeRouter / EdgeOS (Vyatta/`set`-style) support. That is a different dialect closer to
  the Junos `set` model and belongs to a separate unit, not EdgeSwitch.
- UniFi controller / UniFi switch JSON configs. Different format entirely; out of scope.
- A precise EdgeSwitch-vs-Catalyst classifier. Ambiguous captures degrade to `cisco-ios`
  labeling by design.
