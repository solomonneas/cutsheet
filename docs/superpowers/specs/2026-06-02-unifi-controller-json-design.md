# Unit 4 - UniFi Network Controller JSON Config Support

Date: 2026-06-02
Status: Proposed (design only, no implementation in this unit).
Depends on: Unit 1 "Shared Architecture Conventions" (`2026-06-02-l2-switching-semantics-design.md`).

## Goal

Add support for reviewing diffs of **Ubiquiti UniFi Network controller JSON** configuration
exports. UniFi configs are not line-oriented device CLI text; they are JSON documents
(device provisioning objects and/or keyed settings sections such as `networkconf`,
`portconf`, `port_overrides`, `firewallrule`, `routing`, `wlanconf`). This unit adds a new
`unifiJSONParser` that walks the JSON and **flattens it into pseudo-line `configBlock`s** so
the entire existing diff -> analyzer -> report pipeline (`diffBlocks`, the `touched*`
extractors, `riskFindings`, rollback, reports) works unchanged.

This is the most structurally novel unit because the whole codebase assumes
`configBlock.Lines` is `[]string` of human-readable lines. The crux of the design is the
**flattening rule** and the **block-ID stability scheme**, because JSON object/array
ordering is not stable across exports and naive flattening would make every diff look like a
total rewrite. Those two pieces get the most rigor below.

This unit follows the Shared Architecture Conventions from Unit 1:

- New semantics live in their own files; the parser produces `configBlock`s and the analyzer
  and reports consume them. We add a genuinely new dialect (JSON), so a new parser is
  justified (the convention explicitly allows parser work when a new dialect requires it).
- Structured facts before reports: UniFi changes surface through the existing fact types
  (`TouchedVLAN`, `TouchedInterface`, `TouchedRoute`, `TouchedRule`, `SwitchingChange`, etc.)
  by emitting normalized readable pseudo-lines that the existing extractors already match.
- Risk findings reuse `RiskFinding` and flow through the existing `add()` dedup closure.
- Schema changes are additive only. `schema_version` bumps to `"1.1"` (shared with Unit 1).
- Golden tests add a new fixture pair + golden entry; no existing fixture changes.

## Approach / Parser (`internal/configdiff/unifi.go`)

### Parser registration

Add `unifiJSONParser` implementing the existing `Parser` interface:

```go
type unifiJSONParser struct{}
var _ Parser = unifiJSONParser{}
func (unifiJSONParser) Parse(text string, requestedVendor string) parsedConfig
```

`Parse` does `json.Unmarshal([]byte(text), &root)` into `any` (or
`map[string]json.RawMessage` for the keyed-export shape). Because `Explain` reads both the
before and after files and passes them to one selected parser, `Parse` runs once per file
and must be deterministic and order-insensitive (see block-ID scheme).

### Graceful non-JSON fallback (spec'd explicitly)

If `json.Unmarshal` fails (the text is not valid JSON, e.g. someone pointed `--vendor unifi`
at a CLI config), `Parse` must NOT panic or error. It falls back to:

```go
parsed := parseGeneric(text, requestedVendor)
parsed.Detection.Parser = "unifi-json"
parsed.Detection.DetectedVendor = "ubiquiti"
parsed.Detection.Confidence = 0.30                     // low: requested but unparseable as JSON
parsed.Detection.Signals = appendSignal(parsed.Detection.Signals, "requested unifi-json but input was not valid JSON; treated generically")
return parsed
```

This keeps the tool honest (Unit 1 convention: "Unsupported vendors are not claimed as
parsed") and guarantees the pipeline still produces a report. The `auto` path never routes
non-JSON text here (see Detection), so this fallback only fires on an explicit vendor request
against bad input.

### Assumed top-level JSON shape (documented assumption)

Real UniFi exports vary widely (per-controller version, backup vs. API response vs.
hand-assembled). We support two pragmatic shapes and document the assumption in code and
README:

- **Shape A - single device provisioning object:** a top-level JSON object describing one
  device, typically carrying `mac`/`name`/`model` and a `port_overrides` array, and possibly
  `config_network`, `routing`, etc. Detected when the root is an object with device-ish keys.
- **Shape B - keyed settings export:** a top-level JSON object whose values are arrays of
  setting entries, keyed by collection name. Supported keys:
  `networkconf`, `portconf`, `port_overrides`, `firewallrule`, `firewallgroup`, `routing`,
  `wlanconf`, and admin/management settings (`admin`, `settings`, or nested `mgmt`/`snmp`).
  Each entry is an object that usually carries `_id` and/or `name`.

Both shapes are normalized into the same `configBlock` stream. Anything we do not recognize
is still flattened generically (kind `generic`) so no data is silently dropped. The
assumption is stated in the README and in a code comment; we do not attempt to support every
controller version's exact schema.

### Flattening rule (the crux)

Walk the JSON to **leaf scalar values** and emit stable, dotted-path pseudo-lines. The walk:

1. Identify a **container** for each entry (e.g. one `networkconf` element, one
   `port_overrides` element). A container becomes one `configBlock`.
2. Within a container, recurse to leaves. Each leaf emits a pseudo-line of the form
   `<path> = <value>` where `<path>` is the dotted/bracketed path **relative to a stable
   container key, not an array index**. Nested objects extend the dotted path; nested arrays
   of scalars are joined (e.g. `tagged_networkconf_ids = [10,20,30]`); nested arrays of
   objects recurse with their own stable sub-key where one exists.
3. Leaf values are normalized: booleans as `true`/`false`, numbers as their decimal form,
   strings unquoted, null as `null`. Keys are lowercased to match the analyzer's
   `strings.ToLower` line handling. Order of pseudo-lines within a block is **sorted
   deterministically** (by path string) so `blockFingerprint` is stable regardless of JSON
   key order. `mergeRelatedBlocks` + `uniquePreserve` already dedup within a block ID.

Example flattened pseudo-lines for one `networkconf` entry named `IoT`:

```
networkconf[iot].name = IoT
networkconf[iot].vlan = 30
networkconf[iot].purpose = corporate
networkconf[iot].ip_subnet = 198.19.30.1/24
```

And one `port_overrides` entry on a switch (port 10):

```
port_overrides[port_idx=10].native_networkconf_id = iot
port_overrides[port_idx=10].op_mode = switch
port_overrides[port_idx=10].poe_mode = auto
port_overrides[port_idx=10].stp_port_mode = true
port_overrides[port_idx=10].forward = customize
port_overrides[port_idx=10].tagged_vlan_mgmt = block_all
```

### Normalized readable lines (decision: emit CLI-equivalent lines too)

To let the **existing** detectors and the **Unit 1 switching detectors** fire without
JSON-specific duplication, each container ALSO emits a small set of **human-readable
normalized lines** that mimic the CLI vocabulary the analyzer already matches. This is the
key reuse decision: rather than write a parallel JSON risk engine, we translate UniFi
semantics into the line shapes the analyzer keys on.

| UniFi field(s) | Normalized readable line emitted | Picked up by |
| --- | --- | --- |
| `networkconf[x].vlan = N` | `vlan N` (in a `vlan` block) | `touchedVLANs`, `detectPlatform` |
| `port_overrides[x].native_networkconf_id -> vlan N` | `switchport trunk native vlan N` | `interfaceVLANLine`, Unit 1 `native_vlan` |
| `port_overrides[x].tagged_networkconf_ids -> N,M` | `switchport trunk allowed vlan N,M` | `trunkAllowedVLANLine`, Unit 1 `trunk` |
| `op_mode = switch/trunk/aggregate` | `switchport mode access`/`trunk` | Unit 1 `switchport_mode` |
| `stp_port_mode = false` (disabled) | `no spanning-tree portfast` style / STP marker | Unit 1 `spanning_tree` (per-port) |
| `port_overrides[x].forward = disabled` | `shutdown`; else `no shutdown` | `shutdownStateChanged` |
| `routing[x]` default static route | `ip route 0.0.0.0/0 <gw>` | `isDefaultRouteLine`, `touchedRoutes` |
| `firewallrule[x]` accept any->any | `permit ip any any` | `aclBroadeningLine` |
| admin/ssh/snmp settings | `snmp-server community ...` / `ip ssh ...` style | management/observability detectors |

Resolving a `networkconf_id` reference to its VLAN number requires a **pre-pass** that builds
a `networkconf_id -> {name, vlan}` map from the `networkconf` collection before flattening
ports/firewall/routing. This map is built per file (before, after) inside `Parse`. If an ID
cannot be resolved (referenced network absent from the export), emit the raw id and a
`unresolved networkconf_id` note line so the diff still shows movement.

The dotted pseudo-lines (machine-traceable) and the readable lines (detector-friendly) both
live in the same block's `Lines`. Evidence in reports will therefore include both, which is
acceptable and informative.

### Block-ID scheme (stability is mandatory)

Block IDs MUST be derived from a **stable content identifier**, never the JSON array index,
because export order is not guaranteed. The scheme, using `stableID()` for normalization:

| Container | Kind | Block ID | Stable key source |
| --- | --- | --- | --- |
| `networkconf` entry | `vlan` (and `interface` for its SVI/subnet) | `vlan:` + key | `name`, else `_id`, else `vlan` number |
| `portconf` profile | `interface` | `interface:portconf-` + key | `name`, else `_id` |
| `port_overrides` entry | `interface` | `interface:port-` + key | `port_idx` (stable physical port), else `_id` |
| `firewallrule` entry | `firewall` | `firewall:` + key | `name`, else `rule_index`+`ruleset`, else `_id` |
| `firewallgroup` entry | `firewall` | `firewall:fwgroup-` + key | `name`, else `_id` |
| `routing` static route | `route` | `route:` + prefix | destination prefix (e.g. `0.0.0.0/0`) |
| `wlanconf` entry | `management` (SSID/wireless) | `wlan:` + key | `name` (SSID), else `_id` |
| admin/ssh/snmp settings | `management` / `aaa` / `observability` | `management:`/`aaa:`/`observability:` + setting key | setting key path |
| unrecognized container | `generic` | `line:` + `stableID(path)` | dotted path |

Key-selection helper (spec, not code): pick the first present of a documented priority list
per container (`name` > `_id` > domain-specific field like `port_idx`/`vlan`/`prefix`). If
NONE is present, fall back to a content hash of the container's sorted leaves
(`stableID(joinedLeaves)`) so identical content keeps a stable ID across exports while
differing content diffs cleanly. `_id` is included because it is stable within one
controller; `name` is preferred because it survives controller re-adoption where `_id` can
change. This priority is the single most important design decision in the unit.

### `selectParser` ordering

Add vendor aliases and an early auto-detect branch. JSON is unmistakable versus line-based
configs, so the JSON check runs **first** in the `auto` switch (before Fortinet/Cisco/Junos),
avoiding any chance that JSON braces are misread by line heuristics:

```go
case "unifi", "unifi-json", "unifi-controller", "ubiquiti":
    return unifiJSONParser{}, nil
case "auto":
    if looksUnifiJSON(text) {           // FIRST: JSON is unambiguous
        return unifiJSONParser{}, nil
    }
    if looksFortinet(text) { ... }
    // ... existing order unchanged
```

`looksUnifiJSON(text)`:

```
trimmed := strings.TrimSpace(text)
starts := strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
distinctive := contains any of: "networkconf", "port_overrides", "portconf",
              "firewallrule", "wlanconf", "\"_id\"", "site_id", "native_networkconf_id"
return starts && distinctive
```

Both conditions are required so a generic non-UniFi JSON blob does not get mislabeled as
UniFi (it would fail the distinctive-keys check and fall through to `genericParser`, which
treats it as text). The combined before+after text passed to `selectParser` from `Explain`
means the detection sees both files; that is fine since both are the same shape.

## Detection

`unifiJSONParser.Parse` sets:

- `Detection.Parser = "unifi-json"`
- `Detection.DetectedVendor = "ubiquiti"`
- `Detection.DeviceType` derived from content: `switch` if `portconf`/`port_overrides`
  present; `gateway` if `routing`/`firewallrule` present; `wireless` if only `wlanconf`;
  `network-device` otherwise. When both switching and gateway signals appear (a full site
  export), prefer `gateway` (it carries the higher-risk routing/firewall surface).
- `Detection.Confidence`: `0.90` when vendor explicitly requested and JSON parsed; `0.80` on
  the `auto` path; `0.30` on the non-JSON fallback above.
- `Detection.Signals`: include `"unifi controller json export"` plus which collections were
  seen (e.g. `"networkconf/portconf/firewallrule sections"`).

`detectPlatform()` is still called on the produced blocks for signal accumulation, then the
UniFi-specific fields above are overwritten (same pattern Fortinet/Junos parsers use).

## Data Model impact

No NEW fact type is introduced by this unit. UniFi changes reuse:

- `SwitchingChange` (from Unit 1) for native/trunk/op-mode/STP changes, populated by the same
  Unit 1 detectors because we emit CLI-equivalent readable lines. No UniFi-specific switching
  fact is needed.
- `TouchedVLAN` (from `networkconf` blocks and `vlan N` readable lines).
- `TouchedInterface` (from `port_overrides`/`portconf` blocks).
- `TouchedRoute` (from `routing` blocks via the emitted `ip route 0.0.0.0/0 <gw>` lines;
  `routePrefix`/`routeNextHop` already handle `ip route` form).
- `TouchedRule` (from `firewallrule` blocks, kind `firewall`).
- `CategoryChange` for management/aaa/observability blocks.

The single deliberate decision: **normalize-to-readable-lines instead of adding a JSON fact
type.** Rationale: it maximizes reuse of the existing + Unit 1 detectors, keeps the schema
stable, and means UniFi findings render identically to CLI-vendor findings. The cost is that
some pseudo-lines are synthetic (they never appeared verbatim in the JSON); we mitigate this
by ALSO carrying the literal dotted-path leaf lines as evidence, so reports remain traceable
to the source JSON.

Schema: additive only, `schema_version` -> `"1.1"` (shared with Unit 1; if Unit 1 already
bumped it, this unit needs no further schema change beyond ensuring the enum includes
`"1.1"`). No new top-level array is required by this unit alone.

## Risk Findings

All findings reuse `RiskFinding` and flow through the existing `add()` dedup closure in
`riskFindings()`. Because we emit CLI-equivalent lines, most fire automatically from existing
logic. Specific UniFi-relevant triggers:

| Severity | Category | Title | Trigger (post-normalization) |
| --- | --- | --- | --- |
| high | `routing` | Default route changed | `routing` block yields `0.0.0.0/0` line (existing `isDefaultRouteLine`) |
| medium | `routing` | Route removed | a `routing` static route block removed |
| high | `acl_firewall` | ACL or firewall rule appears broadened | `firewallrule` accept with all/any src+dst -> `permit ip any any` |
| high | `management` | Management service may be exposed | firewall rule exposes ssh/https/snmp mgmt port |
| medium | `switching` | Interface VLAN assignment changed | `native_networkconf_id` change -> `switchport ... native vlan N` |
| medium | `switching` | Trunk allowed VLAN list changed | `tagged_networkconf_ids` change -> `trunk allowed vlan` delta |
| high | `switching` (Unit 1) | Switchport mode changed | `op_mode` access<->trunk flip |
| medium | `switching` | VLAN removed | a `networkconf` block removed |
| medium | `interface` | Interface shutdown state changed | `port_overrides[x].forward` disabled<->enabled |
| medium | `management` | Management access changed | admin/ssh/snmp settings block changed |

Plus Unit 1's per-port `spanning_tree` finding if `stp_port_mode` toggles map to a
PortFast/BPDU-style marker line. Each finding carries the dotted-path leaf lines as evidence
and a concrete recommendation, following the existing `add(...)` signature.

## Reports

No new report file. UniFi facts render through the existing renderers:

- `touched-objects.md`: VLANs, interfaces, routes, firewall rules, management changes all
  appear via the existing `writeVLANs`/`writeInterfaces`/`writeRoutes`/`writeRules`/
  `writeCategory` helpers. If Unit 1's `## Switching / L2` section exists, UniFi switching
  changes render there too.
- `risk-analysis.md` / `stakeholder-brief.md`: risk findings render automatically.
- `rollback-plan.md`: rollback uses the default (non-CLI) branch in `rollbackCommands`
  because `parser == "unifi-json"` is not Cisco/Junos/Fortinet. Spec note: the default
  rollback note text ("Reapply these before-config lines...") is correct conceptually but the
  "lines" are JSON pseudo-lines, not pasteable controller commands. We add a UniFi-specific
  case in `rollbackCommands` returning `change.BeforeLines` with the note: *"UniFi controller
  changes are applied via the controller UI/API, not a CLI. These are the before-state field
  values for this object; re-apply them through the controller or restore the prior backup
  after operator review."* This keeps rollback honest about the apply mechanism.
- `validation-plan.md`: existing conditional lines (VLAN, route, firewall, management) cover
  the UniFi cases since they key off the same fact types.

## Testing

- New golden fixture pair `testdata/unifi-before.json` / `testdata/unifi-after.json`: small,
  lab-safe, redacted Shape B export (keyed sections), exercising exactly:
  1. a **VLAN change**: `networkconf` entry `IoT` vlan 30 -> 31.
  2. a **port native-VLAN change**: `port_overrides[port_idx=10].native_networkconf_id`
     moves from `Corp` to `IoT`.
  3. a **firewall rule broadening**: a `firewallrule` source moves from a specific
     group/CIDR to `all`/any with action `accept`.
  4. a **static route change**: `routing` default route `0.0.0.0/0` next-hop/gateway change
     (must trigger the default-route finding).
- New golden `testdata/golden/unifi-summary.json` and a `unifi` case in `golden_test.go`
  (mirrors the cisco/junos cases, `Vendor: "auto"`). `goldenSummary` already covers the
  counts and risk titles needed; if Unit 1 added a `SwitchingChanges int` field, populate it.
- `schema_test.go`: passes against the `"1.1"` schema. Add (or rely on Unit 1 adding) `"1.1"`
  to the `schema_version` enum. Consider an additional schema_test pass using the UniFi
  fixture to confirm JSON-sourced output validates identically.
- Unit tests in `configdiff_test.go`: table-driven tests for `looksUnifiJSON`, the
  flattening of each container kind, block-ID stability (same content in different array
  order -> identical IDs and no diff), `networkconf_id` resolution, and the non-JSON
  fallback path.

## Out of Scope (YAGNI)

- UniFi/Ubiquiti **XML** or `.unf` binary backups (JSON only).
- **Multi-site** exports (a single top-level keyed map per file is assumed; one site).
- **Live controller API fetch** - the privacy model is unchanged: local files only, no
  network access. The tool reads exported JSON files the operator already has on disk.
- Full controller schema validation or version-specific field coverage; we support the
  common collections and flatten the rest generically.
- WLAN/RF semantics beyond SSID presence/management changes (no channel/power analysis).
- Resolving cross-references beyond `networkconf_id -> vlan` (e.g. firewall group membership
  expansion) - groups are diffed as objects, not expanded.
