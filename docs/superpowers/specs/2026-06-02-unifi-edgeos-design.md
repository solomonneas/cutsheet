# Unit 3 - Ubiquiti UniFi EdgeOS / VyOS Gateway Support (set-style)

Date: 2026-06-02
Status: Approved direction (set-style form only; curly-brace form deferred).

Part of the 5-unit multi-vendor expansion. This unit adds a deterministic parser path
for Ubiquiti EdgeOS / VyOS set-style gateway configs (EdgeRouter, USG, UDM-style
gateways). It follows the **Shared Architecture Conventions** established in Unit 1
(`docs/superpowers/specs/2026-06-02-l2-switching-semantics-design.md`, section
"Shared Architecture Conventions"). In particular: parser produces `configBlock`s ->
analyzer diffs blocks and extracts structured facts + risk findings -> deterministic
provider renders reports; new risks reuse `RiskFinding` with a category string and flow
through the existing `add()` dedup closure; schema changes are additive only; new vendors
add a new fixture + golden entry.

## Goal

EdgeOS and VyOS are set/delete command dialects (VyOS is the upstream of EdgeOS). The
`show configuration commands` output is line-oriented `set ...` statements that map
cleanly onto the existing Junos set-line machinery (`classifyJunosSetLine`, `looksJunos`,
`mergeRelatedBlocks`, `normalizeLine`). The goal is to let an operator diff two EdgeOS/VyOS
gateway configs and surface the same structured facts (interfaces, VLANs, routes,
firewall/ACL rules, NAT, VPN, management/AAA/observability) and risk findings the other
vendor paths already produce, including default-route detection.

This is a **new parser dialect** (a genuinely new `set` vocabulary), so parser code is
added here, but it deliberately **reuses** the Junos set-line tokenizer rather than
re-implementing it. No analyzer or report surgery is required: the analyzer keys off
existing block `Kind`s, so mapping EdgeOS paths to the existing kinds is the whole job.

## Approach / Parser (`edgeOSParser`)

Add a fourth set-style parser alongside `junosParser`:

```go
type edgeOSParser struct{}
var _ Parser = edgeOSParser{}
```

`edgeOSParser.Parse` mirrors `junosParser.Parse` exactly:

1. Split on newlines, `normalizeLine` each line (drops blanks, comments, collapses
   whitespace). EdgeOS exports may include a trailing comment marker; `normalizeLine`
   already strips `#`, `//`, `;`, and `!` leading-comment lines.
2. For each non-empty line, call a new `classifyEdgeOSSetLine(line)` (the EdgeOS analogue
   of `classifyJunosSetLine`) returning `(kind, id, header)`.
3. Append a single-line `configBlock` per line, then `mergeRelatedBlocks(blocks)` so all
   lines that resolve to the same block ID (e.g. one interface, one firewall ruleset, one
   static route) collapse into one block with deduped lines.
4. Run `detectPlatform`, then override `Parser = "edgeos"`, `DetectedVendor = "ubiquiti"`,
   confidence `0.80` for `auto` / `0.90` for an explicit vendor request, and append signal
   `"edgeos/vyos set-style syntax"`.

`classifyEdgeOSSetLine` reuses the Junos tokenizer's structure: split into fields, require
`fields[0]` in `{set, delete}` (otherwise fall through to `classifyLine` then `"generic"`),
then match on the EdgeOS path vocabulary below. The verb (`set`/`delete`) is preserved in
the header so add/remove direction survives into the diff, exactly as Junos does. Block IDs
must be **stable and verb-independent** (the ID excludes `set`/`delete`) so that a `set`
in the before file and a `delete`/changed line in the after file land in the same block and
diff correctly. This matches how `classifyJunosSetLine` builds IDs from the path, not the verb.

### Path-vocabulary-to-kind mapping (core of this unit)

All matching is case-insensitive (`strings.EqualFold` on tokens). `ethX` / `swX` / `N`
are placeholders. The ID column shows the stable block ID (verb stripped). `eth0`, `sw0`,
etc. are lowercased via `strings.ToLower`.

| EdgeOS / VyOS path | Kind | Block ID | Notes |
| --- | --- | --- | --- |
| `interfaces ethernet ethX ...` | `interface` | `interface:ethX` | physical port; all sub-statements (address, duplex, description, disable) merge into one block |
| `interfaces switch swX ...` | `interface` | `interface:swX` | switch interface (EdgeRouter switch group) |
| `interfaces bridge brX ...` | `interface` | `interface:brX` | bridge interface |
| `interfaces loopback loX ...` | `interface` | `interface:loX` | loopback |
| `interfaces pppoeX ...` / `interfaces wan ...` | `interface` | `interface:<name>` | WAN/pppoe uplink |
| `interfaces ethernet ethX vif N ...` | `vlan` | `vlan:N` (+ interface block for ethX) | **VLAN sub-interface.** Emit a `vlan` block keyed on the VLAN tag `N` so it shows up in `TouchedVLANs`, and also append the line to the parent `interface:ethX` block so interface facts stay complete. See "VLAN handling" below. |
| `interfaces switch swX switch-port vlan-aware ...` | `vlan` | `vlan:swX-vlan-aware` | switch-port VLAN-aware mode (thin L2, see Unit 1 reuse) |
| `interfaces switch swX switch-port interface ethY vlan N ...` | `vlan` | `vlan:N` | access/trunk VLAN membership on a switch port |
| `protocols static route X.X.X.X/Y next-hop ...` | `route` | `route:X.X.X.X/Y` | static route; default route (`0.0.0.0/0`) still detected by `isDefaultRouteLine` because the prefix appears verbatim in the line |
| `protocols static route6 X::/Y next-hop ...` | `route` | `route:X::/Y` | IPv6 static route; `::/0` triggers default-route risk |
| `protocols static interface-route ...` / `... blackhole ...` | `route` | `route:<prefix>` | interface and blackhole routes |
| `protocols ospf ...` / `protocols bgp ...` | `routing` | `routing:<proto>` | dynamic routing (maps to existing `routing` kind, mirrors Cisco `router ...`) |
| `firewall name <NAME> rule N ...` | `acl` | `acl:<name>` | named IPv4 ruleset; rules merge into one block per ruleset so `touchedRules` can parse action/source/destination |
| `firewall name <NAME> default-action ...` | `acl` | `acl:<name>` | default action line merges into the ruleset block |
| `firewall ipv6-name <NAME> rule N ...` | `acl` | `acl:<name>` | IPv6 named ruleset |
| `firewall group ...` (address-group / network-group / port-group) | `firewall` | `firewall:group-<name>` | reusable groups referenced by rules |
| `firewall ... ` (other global firewall, e.g. `all-ping`, `state-policy`) | `firewall` | `firewall:global-<stableID>` | global firewall posture |
| `service nat rule N ...` | `nat` | `nat:rule-N` | source/destination/masquerade NAT; `masquerade` keyword already hits `isNATLine` |
| `vpn ipsec ...` | `vpn` | `vpn:ipsec-<stableID>` | site-to-site / IKE / ESP |
| `vpn l2tp ...` / `vpn pptp ...` | `vpn` | `vpn:<type>-<stableID>` | remote-access VPN |
| `service ssh ...` | `management` | `management:ssh` | SSH management plane |
| `service gui ...` / `service https ...` | `management` | `management:gui` | web GUI management plane |
| `service telnet ...` | `management` | `management:telnet` | telnet (flag as risky management) |
| `system host-name ...` / `system name-server ...` (host-name -> non-risk) | `management` / `observability` | see Notes | `host-name` is identity (treat as `management`); `name-server` is `observability` |
| `system login user <USER> ...` | `aaa` | `aaa:user-<user>` | local user accounts |
| `system login radius-server ...` / `... tacplus-server ...` | `aaa` | `aaa:<type>-<stableID>` | external auth |
| `service snmp ...` | `observability` | `observability:snmp` | SNMP |
| `service dhcp-server ...` / `service dhcpv6-server ...` | `observability` | `observability:dhcp-<stableID>` | DHCP scopes (operational/observability bucket; not a security-plane risk by default) |
| `system syslog ...` / `system ntp ...` / `system name-server ...` | `observability` | `observability:<facet>-<stableID>` | logging/NTP/DNS, mirrors `isJunosObservabilityLine` |
| anything else | fall through to `classifyLine`, else `generic` | `line:<stableID>` | safety net identical to Junos |

Implementation reuse: `classifyEdgeOSSetLine` should be a thin sibling of
`classifyJunosSetLine` (same field-splitting, same `min(...)` + `stableID(...)` ID helpers,
same final fall-through to `classifyLine`). Where the EdgeOS keyword overlaps an existing
classifier (`service`, `firewall`, `protocols static route`), prefer the explicit EdgeOS
branch so the kind/ID are stable. Small EdgeOS-specific predicate helpers
(`isEdgeOSManagementLine`, `isEdgeOSAAALine`, `isEdgeOSObservabilityLine`) mirror the
`isJunos*Line` helpers and keep each concern line-oriented and lowercased.

### VLAN handling

EdgeOS expresses VLANs as `vif` sub-interfaces (`set interfaces ethernet eth1 vif 20
address 198.18.20.1/24`) and as switch-port VLAN membership. To surface them in the existing
`TouchedVLANs` fact:

- Emit a `vlan` block keyed on the numeric tag (`vlan:20`) AND append the same line to the
  parent `interface:eth1` block. A single source line can produce two block memberships;
  `mergeRelatedBlocks` dedups within each block, and the analyzer's `vlanIDs()` regex already
  extracts `vlan 20` / `vif 20`-style tokens. Confirm the existing `vlanIDs` regex matches
  the `vif N` token; if it does not, extend it in the analyzer (additive, no behavior change
  for other vendors) rather than special-casing EdgeOS in the parser.
- Recommended: keep the parser emitting one block per line and let `classifyEdgeOSSetLine`
  return the `vlan` kind for `vif`/`switch-port ... vlan` lines, then rely on
  `touchedInterfaces`/`touchedVLANs` (which scan all change lines) to attribute the line to
  both the interface and the VLAN. This avoids duplicating lines and keeps parser logic flat.

## Detection (`looksEdgeOS`)

Add `looksEdgeOS(text)` modeled on `looksJunos` (count distinctive line prefixes, return
true at a small threshold). Key on EdgeOS/VyOS-distinctive `set` tokens that do **not**
appear in Junos:

| Distinctive token (lowercased prefix) | Why it disambiguates from Junos |
| --- | --- |
| `set interfaces ethernet eth` | Junos uses `interfaces ge-/xe-/et-`, never `ethernet ethN` |
| `set interfaces switch sw` | EdgeRouter switch groups; no Junos analogue |
| `set service gui` | EdgeOS/UniFi web GUI; not a Junos path |
| `set service ssh` / `set service nat` / `set service dhcp-server` | EdgeOS `service` tree (Junos uses `system services`) |
| `set system host-name` | EdgeOS/VyOS spell it `host-name`; Junos uses `system host-name` too, so this is supporting-only, not decisive |
| `set protocols static route` | EdgeOS/VyOS `protocols static`; Junos uses `routing-options static route` |
| `set firewall name` | EdgeOS/VyOS named rulesets; Junos uses `firewall family ... filter` |

Threshold: `score >= 3` (same shape as `looksJunos`). Count `+1` per matching line; lines
like `set interfaces ethernet eth`, `set service gui`, `set protocols static route`, and
`set firewall name` are the strong signals.

### Ordering vs `looksJunos` in `selectParser`

Both EdgeOS and Junos are set-style, so `looksJunos` could also fire on EdgeOS text (e.g.
`set interfaces ...`, `set firewall ...`). **`looksEdgeOS` MUST be checked before
`looksJunos`** in the `auto` branch. The EdgeOS signals (`interfaces ethernet eth`,
`service gui`, `service nat`, `protocols static route`, `firewall name`) are strictly more
specific than the generic `set interfaces` / `set firewall` prefixes that `looksJunos`
counts, so an EdgeOS config that scores >= 3 on `looksEdgeOS` is EdgeOS, and the earlier
check wins. Fortinet and Cisco checks stay ahead of both (they are unambiguous and unrelated
to set-style). Final `auto` order:

```
looksFortinet -> looksCiscoIOS -> looksEdgeOS -> looksJunos -> genericParser
```

### Vendor aliases

In `selectParser`, add a case mapping these aliases to `edgeOSParser{}`:
`edgeos`, `vyos`, `ubiquiti-gateway`, `usg`, `udm`, `edgerouter`. Update the
"unsupported vendor mode" error string to list the new modes.

## Data Model impact

**None required.** This unit reuses existing block kinds (`interface`, `vlan`, `route`,
`routing`, `acl`, `firewall`, `nat`, `vpn`, `management`, `aaa`, `observability`) and
existing facts (`TouchedInterfaces`, `TouchedVLANs`, `TouchedRoutes`,
`TouchedACLFirewallRules`, `TouchedNATObjects`, `TouchedVPNObjects`,
`ManagementPlaneChanges`, `AAAChanges`, `LoggingSNMPNTPDNSChanges`). No new struct, no new
`Analysis` field.

Per the Shared Architecture Conventions, schema changes are additive only and this unit
adds none. The current schema/analyzer hard-code `schema_version` `"1.0"`; if Unit 1 has
already bumped the enum to `"1.1"`, this unit inherits it unchanged. EdgeOS adds no field,
so it neither requires nor forces a version bump on its own. (Ambiguity resolved below.)

## Risk Findings reused

No new `RiskFinding` category. Every risk an EdgeOS diff should raise is already produced
by the existing `riskFindings()` loop once blocks are classified into the right kinds:

| EdgeOS change | Existing risk that fires | How it triggers |
| --- | --- | --- |
| `protocols static route 0.0.0.0/0 next-hop ...` change | "Default route changed" (high) | `isDefaultRouteLine` matches `0.0.0.0/0` / `::/0` in the line |
| static route removed | "Route removed" (medium) | `change.ChangeType == "removed"` + `isRouteLine` (matches `... static route ...` via the `" static route "` substring) |
| `firewall name` rule broadened (`source address 0.0.0.0/0` + `action accept`) | "ACL or firewall rule appears broadened" (high) | `aclOrFirewallBroadening` (`0.0.0.0/0` path + accept). Confirm EdgeOS `action accept` is caught; extend `junosBroadeningLines`/add an edgeOS-aware check if EdgeOS spells acceptance as `action accept` rather than `then accept` (additive helper). |
| firewall rule exposes mgmt port (`destination port 22/443` from any) | "Management service may be exposed" (high) | `exposesManagementPath` / `exposesManagementPort` (extend the `destination-port` matcher to also accept EdgeOS `destination port N` spacing if needed) |
| `service nat rule ...` change | "NAT configuration changed" (medium) | `change.Kind == "nat"` |
| `vpn ipsec ...` change | "VPN peer or tunnel configuration changed" (medium) | `change.Kind == "vpn"` |
| `system login user ...` / radius change | "AAA or authentication changed" (high) | `change.Kind == "aaa"` |
| `service ssh` / `service gui` / `service telnet` change | "Management access changed" (medium) | `change.Kind == "management"` |
| `service snmp` / `system syslog` removed | "Logging or monitoring may be reduced" (medium) | `change.Kind == "observability"` + removed |
| `vif`/switch-port VLAN change | "Interface VLAN assignment changed" / "VLAN removed" (medium) | `interfaceVLANLine` / `vlan` block removed |
| interface `disable` toggled | "Interface shutdown state changed" (medium) | EdgeOS uses `disable`, not `shutdown`; see note below |

Two small **analyzer-side** extensions (additive, vendor-neutral, optional but
recommended) keep parity with the other dialects. Spec them but keep them minimal:

1. **`disable` as shutdown:** EdgeOS disables an interface with `set interfaces ethernet
   eth0 disable` and re-enables by deleting it. Extend `shutdownState`/`shutdownLine` to
   recognize a trailing ` disable` token (and its `delete ... disable` removal) so the
   "Interface shutdown state changed" risk fires. This is additive and harmless for other
   vendors (no IOS/Junos line ends in ` disable` in a shutdown sense; gate on whole-token
   match).
2. **EdgeOS broadening/mgmt-port spacing:** EdgeOS firewall rules use space-separated
   `source address 0.0.0.0/0`, `destination port 22`, `action accept`. If the existing
   substring matchers (which assume `source-address`, `destination-port`, `then accept`)
   miss these, add EdgeOS spellings to `aclOrFirewallBroadening`/`exposesManagementPath`
   helpers. Verify against the golden fixture before deciding the extension is needed.

Rollback: extend `rollbackCommands` in `analyzer.go` with a `case "edgeos"` that mirrors
`junosRollbackCommands` (set-style): for `changed`/`removed` reapply the before `set`
lines; for `added` emit `delete <path>` from each added `set` line (strip the `set ` prefix
exactly like the Junos path). Set-style rollback is the cleanest of any vendor here. Until
that case exists, the `default` branch already returns safe generic guidance, so this is an
enhancement, not a blocker.

## Reports

No report changes. All EdgeOS facts render through the existing sections (interfaces,
VLANs, routes, ACL/firewall, NAT, VPN, management/AAA/observability, risk analysis,
rollback plan, validation plan). The detected platform line will show
`parser: edgeos`, `detected_vendor: ubiquiti`. If Unit 1's switching section
(`writeSwitching`) lands, EdgeOS switch-port VLAN changes flow into it for free because
they carry the `vlan` kind and VLAN evidence; EdgeOS L2 is thinner than Catalyst (no STP /
EtherChannel / VTP vocabulary), so most of that section stays empty for EdgeOS, which is
expected and fine.

## Testing

- **Golden fixtures:** add `testdata/edgeos-before.cfg` and `testdata/edgeos-after.cfg`
  in `set`-style form. Exercise: an `interfaces ethernet eth0/eth1` change (incl. a
  `disable` toggle and an address change), a `vif`/VLAN change, a default route
  (`protocols static route 0.0.0.0/0 next-hop`) change, a non-default static route
  removal, a `firewall name WAN_IN rule` broadening (`source address 0.0.0.0/0` +
  `action accept`), a `service nat rule` change, a `vpn ipsec` change, a `service ssh` /
  `service gui` change, a `system login user` change, and a `service snmp` / `system
  syslog` change. This makes every reused risk fire at least once.
- **Golden summary:** add `testdata/golden/edgeos-summary.json` and a `name: "edgeos"`
  case to the `TestGoldenSummaries` table in `golden_test.go` (vendor `"auto"`, asserting
  `parser: "edgeos"`, `detected_vendor: "ubiquiti"`, and the expected counts/risk titles).
  Generate the golden by running `Explain` once and capturing `toGoldenSummary` output
  (the existing `goldenSummary` shape already covers the fields EdgeOS needs; no new count
  field is required since this unit adds no new fact type).
- **Detection unit tests:** table-driven tests for `looksEdgeOS` (true on the EdgeOS sample,
  false on the Junos/Cisco/Fortinet samples) and for `selectParser("auto", edgeOSText)`
  returning `edgeOSParser`, including the EdgeOS-before-Junos ordering assertion (an EdgeOS
  body must not be claimed by `junosParser`).
- **Classifier unit tests:** table-driven `classifyEdgeOSSetLine` cases asserting kind + ID
  for each row of the vocabulary table (especially default-route prefix preservation and
  firewall ruleset ID stability across `set`/`delete`).
- **Schema test:** `schema_test.go` still validates the Cisco sample; EdgeOS adds no schema
  surface, so it passes unchanged. Optionally add an EdgeOS schema-validation assertion for
  extra coverage, but it is not required.
- **Regression:** confirm existing Cisco/Junos/Fortinet goldens are unchanged (EdgeOS adds
  a parser path and at most additive analyzer helpers gated on whole-token matches, so no
  existing fixture should shift; if a `disable`/broadening helper change alters an existing
  golden, that is a signal the gate is too loose and must be tightened).

## Out of Scope (YAGNI)

- **Curly-brace hierarchical form.** EdgeOS/VyOS also exports a `{ }`-nested config
  (`show configuration`). This unit scopes to the `set`-style form (`show configuration
  commands`) only, which fits the existing line-oriented infra with no new tree walker.
  A lightweight pre-pass that flattens curly-brace hierarchy into `set` lines is feasible
  but **deferred** to keep scope tight: it needs brace tracking, path-stack accumulation,
  and quoting rules that the set-style form sidesteps. Note it in the roadmap, do not build
  it here. If a curly-brace file is supplied to the `edgeos` parser, it will fall through to
  `generic`-style per-line handling rather than producing wrong facts.
- Dynamic routing protocol semantics (OSPF/BGP neighbor/policy analysis) beyond classifying
  the block as `routing`.
- UniFi controller / UDM application-layer JSON config (this unit is gateway CLI config only).
- Full VyOS feature coverage (traffic-policy/QoS, VRF, container, flow-accounting). Classify
  to the nearest existing kind or `generic`; do not model their semantics.
- Vendor-specific deep rollback beyond the set/delete reapply pattern described above.

## Implementation checklist (parser-layer, this unit)

1. `parser.go`: add `edgeOSParser` type + `Parse`, `classifyEdgeOSSetLine`, `looksEdgeOS`,
   `isEdgeOS{Management,AAA,Observability}Line` helpers; add alias + `auto`-ordering cases
   to `selectParser`; extend the unsupported-vendor error string.
2. `analyzer.go`: add `case "edgeos"` to `rollbackCommands`; if golden fixtures prove it
   necessary, extend `shutdownState`/`shutdownLine` for `disable`, and the broadening /
   mgmt-port helpers for EdgeOS spacing. Keep all extensions whole-token gated.
3. `testdata/`: add `edgeos-before.cfg`, `edgeos-after.cfg`,
   `golden/edgeos-summary.json`; add the `edgeos` golden case.
4. `configdiff_test.go`: detection + classifier table tests.
5. `README.md`: add EdgeOS/VyOS to supported input types, vendor modes, and limitations.

## README updates

- Supported Input Types: add Ubiquiti EdgeOS / VyOS set-style gateway configs (EdgeRouter,
  USG, UDM-style) to the parser list.
- Current Limitations: add a line - "EdgeOS/VyOS support is an initial deterministic parser
  path for `set`/`delete` style gateway configs (the `show configuration commands` form);
  the curly-brace hierarchical form is not yet parsed." Add the new vendor modes
  (`edgeos`, `vyos`, `ubiquiti-gateway`, `usg`, `udm`, `edgerouter`) to the vendor-mode list.
- Roadmap: add "curly-brace EdgeOS/VyOS hierarchical form flattening" and "deeper EdgeOS/VyOS
  semantics" near-term items.
```
