/**
 * Client-side companion of operator/pkg/schedule: validates standard
 * 5-field cron expressions and previews the next occurrence in the
 * schedule's IANA timezone. Display-only — the server-side validation
 * (webhook + api-server) stays authoritative.
 */

interface Field {
  min: number;
  max: number;
  names?: Record<string, number>;
}

const FIELDS: Field[] = [
  { min: 0, max: 59 }, // minute
  { min: 0, max: 23 }, // hour
  { min: 1, max: 31 }, // day of month
  {
    min: 1,
    max: 12,
    names: { jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6, jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12 },
  },
  { min: 0, max: 7, names: { sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6 } }, // day of week (7 = sunday)
];

function parseField(spec: string, field: Field): Set<number> | null {
  const out = new Set<number>();
  for (const part of spec.split(',')) {
    let range = part;
    let step = 1;
    const slash = part.indexOf('/');
    if (slash >= 0) {
      range = part.slice(0, slash);
      step = Number(part.slice(slash + 1));
      if (!Number.isInteger(step) || step < 1) return null;
    }
    let lo: number;
    let hi: number;
    if (range === '*' || range === '') {
      lo = field.min;
      hi = field.max;
    } else {
      const bounds = range.split('-');
      if (bounds.length > 2) return null;
      const parse = (s: string) => {
        const named = field.names?.[s.toLowerCase()];
        if (named !== undefined) return named;
        return /^\d+$/.test(s) ? Number(s) : NaN;
      };
      lo = parse(bounds[0]);
      hi = bounds.length === 2 ? parse(bounds[1]) : lo;
      if (Number.isNaN(lo) || Number.isNaN(hi) || lo < field.min || hi > field.max || lo > hi) {
        return null;
      }
    }
    for (let v = lo; v <= hi; v += step) {
      // Cron day-of-week 7 is sunday, same as 0.
      out.add(field.max === 7 && v === 7 ? 0 : v);
    }
  }
  return out.size > 0 ? out : null;
}

export interface CronSets {
  minute: Set<number>;
  hour: Set<number>;
  dom: Set<number>;
  month: Set<number>;
  dow: Set<number>;
  domRestricted: boolean;
  dowRestricted: boolean;
}

/** parseCron returns the matching sets, or null when invalid. */
export function parseCron(expr: string): CronSets | null {
  const parts = expr.trim().split(/\s+/);
  if (parts.length !== 5) return null;
  const sets = parts.map((p, i) => parseField(p, FIELDS[i]));
  if (sets.some((s) => s === null)) return null;
  return {
    minute: sets[0]!,
    hour: sets[1]!,
    dom: sets[2]!,
    month: sets[3]!,
    dow: sets[4]!,
    domRestricted: parts[2] !== '*',
    dowRestricted: parts[4] !== '*',
  };
}

/** validateCron returns true when expr is a valid 5-field expression. */
export function validateCron(expr: string): boolean {
  return parseCron(expr) !== null;
}

/** validateTimezone returns true for a resolvable IANA name. */
export function validateTimezone(tz: string): boolean {
  if (!tz) return false;
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

interface ZonedParts {
  y: number;
  mo: number;
  d: number;
  h: number;
  mi: number;
}

function zonedParts(instant: Date, tz: string): ZonedParts {
  const fmt = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
  const get = (type: string) =>
    Number(fmt.formatToParts(instant).find((p) => p.type === type)?.value ?? NaN);
  return { y: get('year'), mo: get('month'), d: get('day'), h: get('hour') % 24, mi: get('minute') };
}

// utcFromZoned inverts zonedParts: the UTC instant showing the given wall
// clock in tz (standard two-pass offset guess; DST gaps resolve nearby).
function utcFromZoned(p: ZonedParts, tz: string): Date {
  const want = Date.UTC(p.y, p.mo - 1, p.d, p.h, p.mi);
  let guess = want;
  for (let i = 0; i < 2; i++) {
    const zp = zonedParts(new Date(guess), tz);
    guess += want - Date.UTC(zp.y, zp.mo - 1, zp.d, zp.h, zp.mi);
  }
  return new Date(guess);
}

const SCAN_DAYS = 8; // mirrors the operator's scan horizon

/**
 * nextOccurrence returns the earliest activation of any expression
 * strictly after `from`, or null (invalid input / nothing within the
 * scan horizon).
 */
export function nextOccurrence(exprs: string[], tz: string, from = new Date()): Date | null {
  if (!validateTimezone(tz)) return null;
  const crons = exprs.map(parseCron);
  if (crons.length === 0 || crons.some((c) => c === null)) return null;

  const start = zonedParts(from, tz);
  let best: Date | null = null;
  for (let dayOffset = 0; dayOffset <= SCAN_DAYS; dayOffset++) {
    // Calendar arithmetic on the zoned date (UTC normalization handles
    // month ends); dow from the same normalized date.
    const dayUTC = new Date(Date.UTC(start.y, start.mo - 1, start.d + dayOffset));
    const y = dayUTC.getUTCFullYear();
    const mo = dayUTC.getUTCMonth() + 1;
    const d = dayUTC.getUTCDate();
    const dow = dayUTC.getUTCDay();
    for (const cron of crons) {
      if (!cron!.month.has(mo)) continue;
      // Standard cron rule: when BOTH dom and dow are restricted, either
      // may match; otherwise both must (an unrestricted field always does).
      const domOK = cron!.dom.has(d);
      const dowOK = cron!.dow.has(dow);
      const dayOK =
        cron!.domRestricted && cron!.dowRestricted ? domOK || dowOK : domOK && dowOK;
      if (!dayOK) continue;
      for (const h of [...cron!.hour].sort((a, b) => a - b)) {
        for (const mi of [...cron!.minute].sort((a, b) => a - b)) {
          const at = utcFromZoned({ y, mo, d, h, mi }, tz);
          if (at.getTime() > from.getTime() && (!best || at < best)) {
            best = at;
          }
        }
        if (best && dayOffset === 0) break; // hours are sorted: first hit of the day wins
      }
    }
    if (best) return best;
  }
  return best;
}
