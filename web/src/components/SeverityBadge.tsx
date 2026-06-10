import type { Severity } from "../api";

// Severity colors match the notifier exactly:
// high=#E74C3C, medium=#E67E22, low=#F1C40F, none=gray.
export function SeverityBadge({ severity }: { severity: Severity | string }) {
  const sev = ["none", "low", "medium", "high"].includes(severity)
    ? (severity as Severity)
    : "none";
  return <span className={`sev-badge sev-${sev}`}>{sev}</span>;
}
