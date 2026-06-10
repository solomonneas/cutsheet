# Unit 5 - Palo Alto PAN-OS Firewall Config Support (set-style)

Date: 2026-06-02
Status: Approved direction (set-style first; XML export out of scope).

## Goal

Add deterministic understanding of Palo Alto Networks PAN-OS firewall configs to the
analyzer so PAN-OS before/after diffs surface the firewall-specific risks operators care
about during change review: security-rule broadening (`source any` + `destination any` +
`action allow`), management-service exposure (ssh/https/snmp reachable from any), NAT
changes, default-route changes, and admin/management-plane changes.

PAN-OS configs are exported in two shapes: `set`-style CLI output (line-oriented, set-command
form like Junos) and XML (`show config running` / config export). **This unit scopes to the
`set`-style format only.** It is line-oriented and fits the existing junos set-line tokenizer
and the Fortinet-derived firewall risk model with no new infrastructure. XML export is
explicitly future work (see Out of Scope) and would require an XML walker analogous to the
JSON flattening introduced in Unit 4.

This unit follows the **Shared Architecture Conventions** established in
`docs/superpowers/specs/2026-06-02-l2-switching-semantics-design.md` (Unit 1): parser
produces `configBlock`s, the analyzer diffs blocks and emits structured facts plus
`RiskFinding`s through the existing `add()` dedup closure in `riskFindings()`, schema is
additive-only, golden tests gate count/risk-title changes, and each new concern lives in a
small, lowercased, `strings.ToLower`-normalized, line-oriented predicate function.

## Approach / Parser (`panosParser`)

Add `panosParser` to `parser.go`, structured exactly like `junosParser.Parse`: split lines,
`normalizeLine`, classify each `set ...` line into a `configBlock` via a new
`classifyPANOSSetLine(line)`, then `mergeRelatedBlocks`, then `detectPlatform`. PAN-OS
`set`-style is single-line set-command form (no indentation-based nesting in the export), so
the per-line block model is the right fit, and multiple `set` lines for the same rule/object
collapse into one block because their derived `ID` is identical (the existing
`mergeRelatedBlocks` ID-merge handles this, same as Junos).

`classifyPANOSSetLine` reuses the junos tokenizer style (`strings.Fields`, verb check for
`set`/`delete`) but maps the PAN-OS path vocabulary to existing block kinds. The block `ID`
keys on the rule/object **name** so all attribute lines for one rule land in one block and the
broadening detector sees `from`/`to`/`source`/`destination`/`application`/`service`/`action`
together.

### Path-vocabulary-to-kind mapping

| PAN-OS set path | Block kind | ID key (collapses on) | Notes |
| --- | --- | --- | --- |
| `set rulebase security rules <NAME> ...` | `firewall` | `firewall:` + rule name | **Core.** Carries `from`/`to` zones, `source`, `destination`, `application`, `service`, `action allow\|deny\|drop` into block Lines so the broadening + management-exposure detectors fire. |
| `set rulebase nat rules <NAME> ...` | `nat` | `nat:` + rule name | Source/destination NAT translate config. |
| `set address <NAME> ...` | `firewall` | `firewall:addr-` + name | Address objects. Reuse firewall/object handling (kept as `firewall` so `touchedRules` surfaces them and broadening detection can read an object that is `ip-netmask 0.0.0.0/0`). |
| `set address-group <NAME> ...` | `firewall` | `firewall:addrgrp-` + name | Address groups (static members / dynamic match). |
| `set service <NAME> ...` | `firewall` | `firewall:svc-` + name | Service objects (protocol/port). Note: PAN-OS `service` is an object, not a rule attribute keyword conflict; the in-rule reference is `set ... rules <NAME> service <REF>`. |
| `set service-group <NAME> ...` | `firewall` | `firewall:svcgrp-` + name | Service groups. |
| `set zone <NAME> ...` | `interface` | `interface:zone-` + name | Security zones (zone-to-interface binding). Treated as interface-plane topology. |
| `set network interface <TYPE> <NAME> ...` | `interface` | `interface:` + lower(name) | Physical/logical interface + IP. |
| `set network virtual-router <VR> routing-table ip static-route <NAME> ...` | `route` | `route:` + dest prefix (or name) | **Default-route detection on `destination 0.0.0.0/0` must fire** via the existing `isDefaultRouteLine`. Prefix extracted from the `destination <CIDR>` token (see Data Model). |
| `set deviceconfig system ssh ...` | `management` | `management:` + stableID | SSH service / mgmt access. |
| `set deviceconfig system snmp-setting ...` | `observability` | `observability:` + stableID | SNMP / telemetry. |
| `set deviceconfig system login-banner ...` | `management` | `management:` + stableID | Banner / login config. |
| `set deviceconfig system service disable-telnet\|disable-http ...` | `management` | `management:` + stableID | Plaintext mgmt service toggles. |
| `set mgt-config users <NAME> ...` | `aaa` | `aaa:user-` + name | Local admin accounts, password-hash, permissions. |
| `set deviceconfig system server-profile syslog ...` / `... ntp-servers ...` / `... dns-setting ...` | `observability` | `observability:` + stableID | Syslog / NTP / DNS. |
| anything else | fall through to `classifyLine`, else `generic` | | Mirrors the junos fallback tail. |

Header strings follow the Junos convention: `<verb> <path-prefix> <name>` (for example
`set rulebase security rules ALLOW-WEB`), so reports and rollback notes read naturally.

`delete ...` forms classify identically to their `set ...` counterparts (same as Junos),
which keeps rollback symmetric.

Detection labeling (set on the returned `parsedConfig.Detection`, like the other vendor
parsers):

- `Parser = "panos"`
- `DetectedVendor = "paloalto"`
- `DeviceType = "firewall"`
- `Confidence = 0.82` for auto, `0.90` when vendor explicitly requested.
- Signal appended: `"pan-os set-style syntax"`.

## Detection (`looksPANOS` + `selectParser`)

Add vendor aliases to `selectParser`'s switch: `paloalto`, `palo-alto`, `panos`, `pan-os`,
`pan` all return `panosParser{}`. Update the `default` error string to list them.

Add `looksPANOS(text)` keyed on PAN-OS-distinctive `set ...` tokens (case-insensitive,
line-prefix scored, threshold `>= 3` matching the other `looks*` helpers):

| Token prefix | Weight |
| --- | --- |
| `set rulebase security rules` | 2 |
| `set rulebase nat` | 2 |
| `set deviceconfig` | 2 |
| `set network virtual-router` | 1 |
| `set zone ` | 1 |
| `set mgt-config` | 1 |
| `set address ` / `set service ` | 1 |

**Ordering in the `auto` branch is load-bearing.** PAN-OS, Junos, and EdgeOS all use
`set ...` lines, so `looksFortinet` (config/edit) stays first, then **`looksPANOS` must be
checked before `looksJunos`** because PAN-OS-distinctive prefixes (`set rulebase`,
`set deviceconfig`, `set mgt-config`, `set zone`) never appear in Junos, whereas a naive
`set ...` test would let Junos win. The required order in `selectParser`'s `auto` case:

```
looksFortinet  ->  looksPANOS  ->  looksCiscoIOS  ->  looksJunos  ->  genericParser
```

`looksPANOS` deliberately requires PAN-OS-only path heads (not bare `set`), so a Junos config
scores 0 against it and falls through to `looksJunos`. (If a future EdgeOS unit lands it slots
after `looksJunos` with its own `set ... ethernet`/`set service nat` heads; note that here as a
known overlap to revisit.)

## Data Model impact

No new fact types and no schema-required-key changes. PAN-OS reuses the existing `Analysis`
arrays:

- Security rules -> `TouchedACLFirewallRules` (via `touchedRules`, kind `firewall`).
- NAT rules -> `TouchedNATObjects` (via `touchedObjects(changes, "nat")`).
- Static routes -> `TouchedRoutes`.
- Interfaces/zones -> `TouchedInterfaces`.
- Admin users -> `AAAChanges`; deviceconfig system -> `ManagementPlaneChanges` /
  `LoggingSNMPNTPDNSChanges`.

Two small helper additions (analogous to `fortinetRoutePrefix` / `fortinetRouteNextHop`)
so `touchedRoutes` can describe PAN-OS static routes whose prefix is not in Cisco
`ip route` form:

- `panosRoutePrefix(lines)` -> returns the token after `routing-table ip static-route <NAME> destination`.
- `panosRouteNextHop(lines)` -> returns the token after `nexthop ip-address`.

`touchedRoutes` gains a `change.Kind == "route"` branch mirroring the existing Fortinet
branch: when no Cisco-style route line is present, fall back to `panosRoutePrefix` /
`panosRouteNextHop`. The `destination 0.0.0.0/0` line still independently triggers
`isDefaultRouteLine` in `riskFindings`, so the default-route finding does not depend on this
helper.

`schema_version` is bumped to `"1.1"` per the Unit 1 convention (the schema enum is updated to
`["1.0", "1.1"]`). No new properties are strictly required for this unit, but the bump keeps
all five units on a single coordinated schema version; the validator is additive-safe.

## Risk Findings (reusing existing categories)

All PAN-OS risks flow through the existing `riskFindings()` loop and the `add()` dedup
closure. No new `add()` signature, no new report renderer. The work is teaching the existing
firewall/management/routing detectors to recognize PAN-OS token shapes, mirroring how
`fortinetBroadeningLines` extended `aclOrFirewallBroadening`.

### 1. Security-rule broadening (core value)

Add `panosBroadeningLines(lines) bool`, called from `aclOrFirewallBroadening` alongside
`junosBroadeningLines` and `fortinetBroadeningLines`. PAN-OS rule attributes are emitted as
separate `set ... rules <NAME> <attr> <value>` lines collapsed into one block, so the detector
scans the block's lines for the `any` tokens plus an allow action:

```
hasAnySource      := line contains "source any"          (set ... rules <N> source any)
hasAnyDestination := line contains "destination any"     (set ... rules <N> destination any)
hasAnyApplication := line contains "application any"
hasAnyService     := line contains "service any"
hasAllow          := line contains "action allow"
return hasAllow && (hasAnySource || hasAnyDestination || hasAnyApplication || hasAnyService)
```

Match on a normalized `" " + lower` so `from any`/`to any` zone-any does not over-fire on its
own (zone-any alone is common and not the signal); the broadening signal is allow + address/
service `any`. The strongest combination to highlight in details is `source any` +
`destination any` + `action allow` (matches the fixture).

`aclBroadeningDetails` gains a `panosBroadeningLines(lines)` clause appending:
`"Broadening candidate: PAN-OS security rule allows traffic with any source, destination, application, or service scope."`
This fires the existing **high / `acl_firewall` / "ACL or firewall rule appears broadened"**
finding, identical title/category to the other vendors so golden risk-title sets stay
consistent.

### 2. Management-service exposure

Extend `exposesManagementPath` (or add a `panosExposesManagement(lines)` helper invoked from
it, mirroring its current any-source + mgmt-port + accept three-flag pattern) to recognize
PAN-OS rules that allow management apps/services to a management zone from any source:

```
hasAnySource := "source any"
hasMgmtApp   := application/service references ssh | ping/icmp-to-mgmt is excluded |
                "service application-default" with app ssh|panorama|web-browsing(https)|snmp,
                or "service service-https" | "service service-ssh" | port 22/443/161/80
hasMgmtZone  := "to <mgmt-zone>" where the rule's `to` zone is a management/trust-mgmt zone
hasAllow     := "action allow"
return hasAnySource && hasMgmtApp && hasAllow
```

Because PAN-OS uses application names rather than ports, the detector keys primarily on
application tokens `ssh`, `panos-web-interface` / `web-browsing` with https service, `snmp`,
and on explicit service objects `service-ssh` / `service-https`. This fires the existing
**high / `management` / "Management service may be exposed"** finding. `exposesManagementPort`
gains the app-name tokens (`ssh`, `https`, `snmp`, `web-browsing`) so the line-level path also
matches. The management-zone hint (`to mgmt` / `to management`) raises confidence but is not
required when the app is an explicit mgmt app from `source any`.

### 3. Routing

`set network virtual-router <VR> routing-table ip static-route <NAME> destination 0.0.0.0/0`
triggers the existing **high / `routing` / "Default route changed"** via `isDefaultRouteLine`
(already matches `0.0.0.0/0`). A removed static-route block triggers the existing
**medium / `routing` / "Route removed"** because the route lines are present in `BeforeLines`
and `isRouteLine`/the new `panosRoutePrefix` path resolves the prefix.

### 4. NAT, AAA, management, observability (reused as-is)

- `kind == "nat"` blocks fire **medium / `nat` / "NAT configuration changed"**. (PAN-OS NAT
  lines are not Cisco `nat` lines, so `beforeAfterDetails` will rely on the captured evidence;
  the finding still fires off `change.Kind == "nat"`, which is unconditional.)
- `kind == "aaa"` (`set mgt-config users ...`) fires **high / `aaa_auth` / "AAA or authentication changed"**.
- `kind == "management"` (`set deviceconfig system ssh|login-banner|service ...`) fires
  **medium / `management` / "Management access changed"**.
- `kind == "observability"` removal or reduced telemetry fires
  **medium / `monitoring` / "Logging or monitoring may be reduced"**.

No new risk titles are introduced, which keeps the cross-vendor golden risk-title vocabulary
stable.

## Reports

No new report sections or renderers. Because PAN-OS reuses existing `Analysis` arrays and
existing `RiskFinding` titles/categories, all eight reports
(`change-summary.md`, `risk-analysis.md`, `touched-objects.md`, `rollback-plan.md`,
`validation-plan.md`, `operator-checklist.md`, `stakeholder-brief.md`, `diff-analysis.json`)
render PAN-OS facts through the existing helpers (`writeRules`, `writeObjects`, `writeRoutes`,
`writeCategory`, risk renderers). The existing conditional validation-plan lines for firewall,
NAT, routing, and management changes already cover the PAN-OS surface; no validation-plan
edits are required for this unit.

## Rollback

PAN-OS set-style rollback is `delete`-of-`set`, identical in shape to Junos. Add
`rollbackCommands`'s `case "panos"` to dispatch to a `panosRollbackCommands(change)` that is
structurally the same as `junosRollbackCommands`:

- `changed` / `removed`: reapply the captured before-config `set ...` lines (note: "PAN-OS
  candidate commands: reapply these set statements, then `commit`, or use a saved config
  snapshot / `load config` rollback if available, after operator review").
- `added`: convert each `set <path...>` to `delete <path...>` (strip the leading `set `),
  with the analogous note.

Because the transform is identical to Junos, `panosRollbackCommands` may simply delegate to a
shared helper (factor the junos body into a `setStyleRollbackCommands(change, vendorLabel)`
and call it with `"PAN-OS"`); spec leaves the refactor-vs-duplicate choice to implementation,
but the behavior must match the junos delete-of-set semantics.

`rollbackNeedsManualReview` already returns true for kinds `route`, `firewall`, `nat`, `aaa`,
`management`, so PAN-OS security/NAT/route/admin changes are correctly flagged for manual
review. High-severity findings (broadening, mgmt exposure, default route, AAA) drive
`Rollback.Confidence = "risky"` through the existing `rollbackAnalysis` logic.

## Testing

- Unit tests in `configdiff_test.go` (table-driven), covering:
  - `classifyPANOSSetLine` mapping for each row of the vocabulary table.
  - `looksPANOS` positive (PAN-OS sample) and negative (a Junos sample must score 0 / fall
    through to `looksJunos`); assert `selectParser("auto", junosText)` still returns
    `junosParser`.
  - `panosBroadeningLines` true on `source any` + `destination any` + `action allow`; false on
    `from any`/`to any` zone-only.
  - `panosExposesManagement` true on `source any` + ssh/https/snmp app + `action allow`.
  - `panosRoutePrefix` / `panosRouteNextHop` extraction and default-route firing.
  - `panosRollbackCommands` delete-of-set for an added block.
- Golden fixtures `testdata/panos-before.cfg` / `testdata/panos-after.cfg` exercising, in one
  diff: a security-rule broadening (source/dest `any` + `action allow`), a NAT rule change, a
  static default-route next-hop change, and a management/admin change (`mgt-config users` or
  `deviceconfig system ssh`).
- Golden summary `testdata/golden/panos-summary.json` and a `panos` case appended to the table
  in `golden_test.go` (vendor `"auto"`). Expected header fields:
  `"parser": "panos"`, `"detected_vendor": "paloalto"`, `"device_type": "firewall"`.
- `schema_test.go` continues to validate the existing cisco sample against the bumped `1.1`
  schema (the `schema_version` enum gains `"1.1"`); the validator is additive-safe.
- No existing golden fixtures change (cisco/junos configs contain no PAN-OS tokens), so
  cisco-summary.json and junos-summary.json are untouched.

### Proposed fixture sketch (illustrative, not final)

`panos-before.cfg`:

```
set deviceconfig system hostname lab-panos-01
set deviceconfig system ssh enable yes
set mgt-config users admin permissions role-based superuser yes
set zone trust network layer3 ethernet1/2
set network interface ethernet ethernet1/1 layer3 ip 203.0.113.2/30
set network virtual-router default routing-table ip static-route DEFAULT destination 0.0.0.0/0
set network virtual-router default routing-table ip static-route DEFAULT nexthop ip-address 203.0.113.1
set rulebase nat rules SNAT-WEB source-translation dynamic-ip-and-port interface-address interface ethernet1/1
set rulebase security rules ALLOW-WEB from trust
set rulebase security rules ALLOW-WEB to untrust
set rulebase security rules ALLOW-WEB source 198.19.20.0/24
set rulebase security rules ALLOW-WEB destination any
set rulebase security rules ALLOW-WEB application web-browsing
set rulebase security rules ALLOW-WEB service application-default
set rulebase security rules ALLOW-WEB action allow
```

`panos-after.cfg` (broadened rule, new NAT pool, changed default next-hop, added admin):

```
set deviceconfig system hostname lab-panos-01
set deviceconfig system ssh enable yes
set mgt-config users admin permissions role-based superuser yes
set mgt-config users netops permissions role-based superuser yes
set zone trust network layer3 ethernet1/2
set network interface ethernet ethernet1/1 layer3 ip 203.0.113.2/30
set network virtual-router default routing-table ip static-route DEFAULT destination 0.0.0.0/0
set network virtual-router default routing-table ip static-route DEFAULT nexthop ip-address 203.0.113.254
set rulebase nat rules SNAT-WEB source-translation dynamic-ip-and-port translated-address NAT-POOL-1
set rulebase security rules ALLOW-WEB from trust
set rulebase security rules ALLOW-WEB to untrust
set rulebase security rules ALLOW-WEB source any
set rulebase security rules ALLOW-WEB destination any
set rulebase security rules ALLOW-WEB application any
set rulebase security rules ALLOW-WEB service any
set rulebase security rules ALLOW-WEB action allow
```

This produces: broadening (high), default route changed (high), NAT changed (medium), AAA
changed / new admin (high), yielding `rollback_confidence: "risky"`.

## Out of Scope (YAGNI)

- **PAN-OS XML export** (`<config>...</config>` from `show config running` or config export).
  This is the natural follow-on and would reuse an XML flattener in the spirit of Unit 4's
  JSON flattening (walk the element tree to dotted/spaced paths, then feed the same
  classify/diff pipeline). Not in this unit.
- **Panorama** device-group / template / template-stack hierarchy and shared/pre/post-rulebase
  layering. This unit handles a single firewall's local rulebase only.
- **vsys multi-tenancy** (`set vsys vsys2 rulebase ...`). Single-vsys (implicit) configs only;
  multi-vsys path prefixes are not disambiguated.
- **App-ID / Content-ID / security-profile semantics** (URL filtering, AV, vulnerability
  profiles, profile-groups). Profile references are captured as evidence lines but their
  security posture is not evaluated.
- **Full NAT translation math** (bi-directional NAT, U-turn/hairpin detection). NAT changes are
  flagged generically, not modeled.
- **HA, log-forwarding profile, and decryption-policy** deep semantics.
