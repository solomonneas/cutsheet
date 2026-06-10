# Cutsheet

[![CI](https://github.com/solomonneas/cutsheet/actions/workflows/ci.yml/badge.svg)](https://github.com/solomonneas/cutsheet/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

**Cutsheet watches your network device configs and tells you what changed and whether you should be worried.**

It polls your switches, gateways, firewalls, and UniFi controllers on a schedule, keeps every config snapshot in a git-backed history, and runs each change through a deterministic risk analyzer. Instead of a wall of colored diff lines, you get a timeline of changes with findings like "trunk now carries all VLANs" or "firewall rule broadened to any/any", plus rollback and validation plans an on-call engineer can actually use. Everything runs in a single binary on your own hardware; no agent installs, no cloud, and it never pushes config to a device.

<!-- TODO: screenshot of the web UI change timeline -->

## Quick start (Docker)

```bash
git clone https://github.com/solomonneas/cutsheet.git
cd cutsheet
docker compose up -d --build
```

Create an API token (required in Docker, see note below):

```bash
docker compose exec cutsheet cutsheet token create --data-dir /data --name admin
```

Open http://localhost:8633, paste the token in Settings, and add your first device.

> **Why the token is required in Docker:** Cutsheet allows tokenless requests
> from localhost only while zero tokens exist, as a first-run convenience.
> Inside Docker, your browser's requests arrive through the published port and
> reach the container from the Docker bridge network, not loopback, so that
> allowance never applies. Create one token and use it; that also closes the
> tokenless door entirely.

The compose file binds the port to `127.0.0.1` on the host. To reach Cutsheet
from other machines, change the port mapping to `"8633:8633"` after you have
created a token.

### Without Docker

```bash
go build -o cutsheet ./cmd/cutsheet
./cutsheet serve --data-dir ./data
```

The server listens on `127.0.0.1:8633` by default and works tokenless from
the same machine until you create a token with `cutsheet token create`.

## Try it with zero hardware

Demo mode seeds a data directory with four sample devices (Cisco Catalyst
switch, EdgeOS gateway, UniFi controller, FortiGate firewall) and replays a
realistic config change on each, so the timeline shows real risk-analyzed
changes immediately:

```bash
./cutsheet demo --data-dir ./demo-data
./cutsheet serve --data-dir ./demo-data
# open http://localhost:8633
```

Or in Docker, before the volume has any data in it:

```bash
docker compose run --rm cutsheet demo --data-dir /data
docker compose up -d
```

Demo mode refuses to touch a non-empty data directory, so it can never
clobber real monitoring data.

## Adding real devices

Collectors are read-only: they run `show`-style commands or call read APIs.
Cutsheet never writes to a device.

SSH device (EdgeOS example; presets exist for `edgeos`, `vyos`, `cisco-ios`,
`junos`, `fortios`):

```bash
cutsheet device add --data-dir ./data \
  --id branch-gw1 --name "Branch Gateway" --address 198.18.0.1 \
  --collector ssh \
  --config '{"host":"198.18.0.1","username":"audit","password":"REDACTED","preset":"edgeos","host_key":"ssh-ed25519 AAAA..."}' \
  --interval 300
```

UniFi controller:

```bash
cutsheet device add --data-dir ./data \
  --id campus-unifi --name "Campus Controller" --address 198.18.0.20 \
  --collector unifi \
  --config '{"url":"https://198.18.0.20","username":"audit","password":"REDACTED","site":"default"}'
```

Notes:

- Passwords and private keys are encrypted at rest (NaCl secretbox) the
  moment the device is added. Set `CUTSHEET_SECRET_KEY` (64 hex chars) to
  control the key yourself; otherwise one is generated at
  `<data-dir>/secret.key` with owner-only permissions.
- SSH host keys are verified against the configured `host_key`. Skipping
  verification requires an explicit `"insecure_ignore_host_key": true`.
- `--interval` is the poll interval in seconds (`0` = manual snapshots
  only). You can also trigger a snapshot any time from the UI or with
  `POST /api/v1/devices/{id}/snapshot`.
- Devices can equally be managed through the web UI or the REST API.

## Notifications

Cutsheet can push every analyzed change to a generic webhook (JSON POST) and
to Discord, filtered by severity:

```bash
cutsheet serve --data-dir ./data \
  --webhook-url https://example.com/hook \
  --discord-webhook-url https://discord.com/api/webhooks/... \
  --notify-min-severity medium
```

The same settings are read from `CUTSHEET_WEBHOOK_URL`,
`CUTSHEET_DISCORD_WEBHOOK_URL`, and `CUTSHEET_NOTIFY_MIN_SEVERITY` (flags
win). Severity ladder: `none` < `low` < `medium` < `high`; the default floor
is `low`, meaning any change with at least one finding.

## How it works

```
scheduler -> collector (SSH / UniFi API / file) -> git snapshot store
                                                        |
                                            change detected on commit
                                                        v
   web UI + REST API <- SQLite (devices, changes, findings) <- risk analysis
            |                                                  (pkg/configdiff)
            +-> notifier (webhook / Discord)
```

Every poll fetches the device's full config. If it differs from the last
snapshot, the change is committed to a per-device path in a git repo, then
analyzed by [`pkg/configdiff`](docs/parsers.md), a deterministic, offline
analysis library with a stable JSON schema. Each change gets a report bundle
(risk analysis, rollback plan, validation plan, operator checklist,
stakeholder brief, HTML view) stored on disk and served through the UI.

Vendor support:

| Parser path | Vendor modes | Input shape | Notes |
| --- | --- | --- | --- |
| Generic | `auto`, `generic` | Plain text | Baseline section and line diffing for unsupported vendors |
| Cisco IOS/IOS XE | `cisco-ios`, `ios`, `ios-xe`, `cisco` | CLI text | Includes Catalyst-oriented Layer 2 switching semantics |
| Ubiquiti EdgeSwitch | `ubiquiti`, `edgeswitch`, `ubiquiti-edgeswitch`, `ubiquitios`, `edgeswitch-cli` | CLI text | Uses IOS-style parsing with EdgeSwitch detection |
| Ubiquiti EdgeOS/VyOS | `edgeos`, `vyos`, `ubiquiti-gateway`, `usg`, `udm`, `edgerouter` | `set` and `delete` command text | Targets gateway configs from `show configuration commands` |
| Palo Alto PAN-OS | `paloalto`, `palo-alto`, `panos`, `pan-os`, `pan` | `set` command text | Targets set-style PAN-OS configs |
| Juniper Junos | `juniper`, `junos` | `set` and `delete` command text | Initial deterministic Junos parser path |
| Fortinet FortiGate/FortiOS | `fortinet`, `fortigate`, `fortios` | `config` and `edit` block text | Initial deterministic FortiOS parser path |
| UniFi Network controller | `unifi`, `unifi-json`, `unifi-controller` | JSON export | Flattens JSON into stable pseudo-lines and readable CLI-equivalent lines |

See [docs/parsers.md](docs/parsers.md) for extraction coverage, the full
risk-finding list, and per-vendor limitations.

## Offline diff CLI

The analysis engine also ships as a standalone tool for one-off change
review with two config files and no server:

```bash
go build -o cutsheet-cli ./cmd/cutsheet-cli
cutsheet-cli explain --before before.cfg --after after.cfg --vendor auto --out ./reports/change-001
```

| Flag | Meaning |
| --- | --- |
| `--before` | Path to the before config |
| `--after` | Path to the after config |
| `--vendor` | Parser mode from the table above, or `auto` |
| `--out` | Output directory for the report bundle |

The bundle contains `diff-analysis.json` (stable schema v1.1),
`change-summary.md`, `risk-analysis.md`, `touched-objects.md`,
`rollback-plan.md`, `validation-plan.md`, `operator-checklist.md`,
`stakeholder-brief.md`, and `report.html` for browser review.

## Security model

- **Read-only by design.** Collectors fetch config; there is no code path
  that pushes config to a device. This is permanent, not a roadmap item.
- **Credentials encrypted at rest.** Device passwords and SSH keys are
  sealed with NaCl secretbox before they touch the database. API responses
  never include them, even encrypted.
- **Token auth.** API access uses bearer tokens (`cutsheet token create`);
  only salted hashes are stored, validated in constant time. Tokens are
  managed from the CLI only, so an API-level attacker cannot mint tokens.
- **Local data.** Snapshots, analysis, and report files live in your data
  directory. No telemetry, no external calls except the webhooks you
  configure.
- **Loopback by default.** The server binds `127.0.0.1` unless you say
  otherwise; the compose file publishes the port on `127.0.0.1` too.

## Roadmap

- Compliance packs: CIS/NIST rule sets per vendor, drift against golden
  configs, audit evidence export.
- AWS network state (security groups, route tables, NACLs) in the same
  change timeline as your switches and firewalls.
- Remote-site collector daemons (outbound-only, MSP-friendly) and
  syslog-triggered instant snapshots.

## Development

```bash
make test    # go test ./...
make vet
make build   # builds ./cutsheet (server) and ./cutsheet-cli (diff CLI)
make ui      # rebuild the embedded web UI after changing web/src
make demo    # seed ./demo-data with sample devices
```

## License

Apache-2.0. See [LICENSE](LICENSE).
