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

## 2026-06-09 - Device registry, snapshot store, scheduler, server skeleton

- **SQLite instead of Postgres for v1.** The design spec says Postgres, but v1
  optimizes for adoption: a single binary with `modernc.org/sqlite` (pure Go,
  no cgo) means `cutsheet serve --data-dir ./data` works with zero external
  services. All persistence is behind the `internal/store` API, so swapping in
  Postgres for multi-tenant in v1.x is an implementation change, not an API
  change. Embedded SQL migrations (go:embed + schema_migrations table) keep the
  upgrade path honest from day one.
- **Single SQLite connection** (`SetMaxOpenConns(1)`) plus WAL and
  busy_timeout: modernc's driver returns SQLITE_BUSY under pooled concurrent
  writers; one connection sidesteps it at v1's write volume.
- **Snapshot store compares against HEAD, not the worktree.** Save() reads the
  previous content from the HEAD commit tree, so a dirty or hand-edited
  worktree file can't suppress (or fabricate) change detection. PrevCommitHash
  comes from `git log -- <file>` semantics (go-git LogOptions.FileName), so
  devices never see each other's commits. Save() is mutex-serialized: one repo,
  many device goroutines.
- **Collector factory is a registration map.** v1 ships only "file"
  (fixture-driven tests + zero-hardware demo mode); ssh/unifi slot in as new
  map entries in a later unit. Collector config is validated at `device add`
  time, not first poll.
- **Scheduler is deliberately dumb:** one ticker goroutine per enabled device
  with interval > 0, reconciled by Refresh() (stop removed/changed, start
  new). No cron parsing. Poll errors log and continue; a failed collector
  build logs and leaves the device unpolled until the next Refresh. 60s fetch
  timeout default. Tests inject sub-second intervals via Options.Interval
  instead of sleeping on real-time PollIntervalSeconds.
- **Change handler is a callback** (`func(ctx, device, SaveResult)`); the
  serve command just logs for now. The analysis pipeline (configdiff Explain +
  RecordChange + reports) plugs into that seam next.
- **go.mod jumped to go 1.25.0** because current go-git/modernc releases
  require it; toolchain auto-download handles the local 1.22 install.

## 2026-06-09 - Analysis pipeline (snapshot change -> recorded findings)

- **Temp-file bridge to Explain().** `configdiff.Explain` is path-based and
  must stay untouched (stable public API), so `internal/pipeline` writes
  `SaveResult.PrevContent` and the current content to a throwaway
  `os.MkdirTemp` dir and removes it after the call. The report dir is the
  product artifact and is kept.
- **Report dir naming:** `<reportsRoot>/<deviceID>/<UTC YYYYMMDD-HHMMSS>-<8-char
  commit hash>`. Timestamp sorts naturally, the short hash disambiguates and
  ties the bundle back to the snapshot commit. The serve command roots reports
  at `<data-dir>/reports`.
- **Severity ladder** none < low < medium < high lives in the pipeline (rank
  map), not configdiff: configdiff emits per-finding severities, the rollup is
  a storage/UX concern. Unknown severity strings rank as none, so a future
  configdiff severity can't crash the rollup (it would just rank low until the
  map learns it).
- **Summary line format:** `"3 findings (1 high) - 5 blocks changed"`, where
  the parenthetical counts findings at the max severity tier. Zero-finding
  changes record `"no findings - N blocks changed"`.
- **First snapshots get a change row too** (summary "initial snapshot",
  severity none, `{}` analysis, no report dir): the timeline shows when
  monitoring began, and the scheduler's Changed=true on first save means the
  handler fires anyway.
- **Handler reloads content via snaps.GetAt(commit)** rather than widening the
  scheduler's ChangeHandler signature with a content param; keeps the seam
  stable and reads exactly the committed bytes.
- **HandleChange returns the recorded Change** (with the new row id) so the
  upcoming REST/notifier layers can use it without a re-query.

## 2026-06-09 - UniFi + SSH collectors, credentials encrypted at rest

- **Secrets format and key resolution.** `internal/secrets` wraps NaCl
  secretbox; values are self-describing `enc:v1:<base64 nonce+box>` strings so
  they live inside collector config JSON unchanged. Key resolution:
  `CUTSHEET_SECRET_KEY` env (64 hex chars) wins; otherwise a key is
  auto-generated at `<data-dir>/secret.key` (0600) on first use and loaded
  thereafter. Tradeoff, on purpose: the auto-generated file means anyone with
  the whole data dir has the key, but the boomer-friendly default (no env-var
  ceremony) still protects against partial leaks (db-only backups, SQL access,
  copied registry dumps). Document `CUTSHEET_SECRET_KEY` as the hardening
  knob, not a requirement.
- **Encryption boundary is `device add`, decryption boundary is Fetch.** The
  collector factory takes an optional `*secrets.Box`; `New(type, config, nil)`
  still works for validation because constructors never decrypt. Plaintext
  credentials in config pass through `decryptIfNeeded` untouched, so dev/test
  setups and pre-encryption configs keep working. `collector.EncryptConfig`
  knows each type's sensitive fields (unifi: password; ssh: password +
  private_key) and is a byte-for-byte no-op for types without any, so `file`
  device adds never even generate a secret key.
- **UniFi collector output determinism is the contract.** Controllers do not
  guarantee array order, and any byte flutter would make every poll look like
  a change. Output is one JSON document marshaled from a fixed-order struct
  (top-level keys: networkconf, portconf, port_overrides, firewallrule,
  firewallgroup, routing, wlanconf - exactly the sections the configdiff
  unifi-json parser keys on), object keys sorted by encoding/json's map
  ordering, arrays sorted by `_id` with a compact-JSON fallback for entries
  without one (port_overrides). Golden + shuffled-server tests pin this.
- **UniFi endpoints:** `/api/s/<site>/rest/{networkconf,portconf,firewallrule,
  firewallgroup,routing,wlanconf}` plus `/api/s/<site>/stat/device`, from
  which all devices' `port_overrides` arrays are flattened into the one
  top-level key the parser expects. Empty sections marshal as `[]`, keeping
  the shape stable.
- **UniFi auth auto-detect:** try UniFi OS first (`POST /api/auth/login`,
  API prefix `/proxy/network`), fall back to legacy (`POST /api/login`, no
  prefix). `unifi_os: true|false` pins the style and skips the probe.
  Cookie-jar session; the `X-CSRF-Token` from the OS login response is echoed
  on subsequent requests (some UniFi OS versions require it, harmless
  elsewhere).
- **SSH host-key policy:** verify against the configured `host_key` (openssh
  authorized-key format) via `ssh.FixedHostKey`; skipping verification
  requires the literal `insecure_ignore_host_key: true`. There is no silent
  fallback to InsecureIgnoreHostKey, and config without either is rejected at
  add time.
- **SSH presets** map to the show command whose output the matching configdiff
  parser consumes (edgeos/vyos share the vyatta wrapper command). Explicit
  `command` overrides the preset; the preset still drives the vendor default
  at `device add` (`--vendor` omitted: unifi -> unifi-json, ssh preset ->
  same-named vendor mode). Explicit `--vendor` always wins (flag.Visit
  detection, so `--vendor auto` is respected as an explicit choice).
- **In-process SSH test server gotcha:** after writing output and sending
  `exit-status`, the server must close the channel or the client's stdout
  reader never sees EOF and `session.Output` hangs forever (found as a 600s
  test timeout, not a clever insight).
