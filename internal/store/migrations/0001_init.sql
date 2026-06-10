CREATE TABLE devices (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    vendor TEXT NOT NULL,
    address TEXT NOT NULL DEFAULT '',
    collector_type TEXT NOT NULL,
    collector_config TEXT NOT NULL DEFAULT '{}',
    poll_interval_seconds INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE changes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    detected_at TEXT NOT NULL,
    commit_hash TEXT NOT NULL,
    prev_commit_hash TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    max_severity TEXT NOT NULL DEFAULT 'none',
    analysis_json TEXT NOT NULL DEFAULT '',
    report_dir TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_changes_device_detected ON changes(device_id, detected_at DESC);

CREATE TABLE risk_findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    change_id INTEGER NOT NULL REFERENCES changes(id) ON DELETE CASCADE,
    finding_id TEXT NOT NULL,
    severity TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    recommendation TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_risk_findings_change ON risk_findings(change_id);
