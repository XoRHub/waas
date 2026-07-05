// Minimal Kubernetes resource.Quantity helpers for the sizing sliders.
// Only the units the platform actually emits are supported (m for CPU,
// binary suffixes for memory) — not the full Quantity grammar.

const MEMORY_UNITS: Record<string, number> = {
  Ki: 1024 ** 1,
  Mi: 1024 ** 2,
  Gi: 1024 ** 3,
  Ti: 1024 ** 4,
  K: 1000 ** 1,
  M: 1000 ** 2,
  G: 1000 ** 3,
  T: 1000 ** 4,
};

/** Parses a CPU quantity ("500m", "2") into millicores. */
export function parseCpu(q: string): number {
  const s = q.trim();
  if (s.endsWith('m')) return Math.round(Number(s.slice(0, -1)));
  return Math.round(Number(s) * 1000);
}

/** Formats millicores back into a quantity ("1500" -> "1500m", "2000" -> "2"). */
export function formatCpu(milli: number): string {
  return milli % 1000 === 0 ? String(milli / 1000) : `${milli}m`;
}

/** Renders millicores for display ("1500" -> "1.5"). */
export function displayCpu(milli: number): string {
  return String(milli / 1000);
}

/** Parses a memory quantity ("512Mi", "2Gi", "1073741824") into bytes. */
export function parseMemory(q: string): number {
  const s = q.trim();
  const match = /^([0-9.]+)([A-Za-z]*)$/.exec(s);
  if (!match) return NaN;
  const [, num, unit] = match;
  return Math.round(Number(num) * (unit ? (MEMORY_UNITS[unit] ?? NaN) : 1));
}

/** Formats bytes into the tightest binary suffix ("2Gi", "1536Mi"). */
export function formatMemory(bytes: number): string {
  if (bytes % MEMORY_UNITS.Gi === 0) return `${bytes / MEMORY_UNITS.Gi}Gi`;
  if (bytes % MEMORY_UNITS.Mi === 0) return `${bytes / MEMORY_UNITS.Mi}Mi`;
  return `${Math.round(bytes / MEMORY_UNITS.Ki)}Ki`;
}

/** Renders bytes for display in Gi with one decimal ("1610612736" -> "1.5 Gi"). */
export function displayMemory(bytes: number): string {
  const gi = bytes / MEMORY_UNITS.Gi;
  return `${Math.round(gi * 10) / 10} Gi`;
}
