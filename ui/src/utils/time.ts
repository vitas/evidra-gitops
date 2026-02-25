/** Format an ISO timestamp string as "YYYY-MM-DD HH:MM:SS UTC". Returns the raw value if unparseable. */
export function fmtDate(v: string): string {
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return d.toISOString().replace("T", " ").replace("Z", " UTC");
}
