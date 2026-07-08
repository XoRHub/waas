import { describe, expect, it } from 'vitest';
import { nextOccurrence, validateCron, validateTimezone } from './cron';

describe('validateCron', () => {
  it('accepts standard 5-field expressions', () => {
    for (const ok of [
      '0 8 * * 1-5',
      '*/15 * * * *',
      '0 22 * * *',
      '30 6 1,15 * *',
      '0 9 * * mon-fri',
    ]) {
      expect(validateCron(ok), ok).toBe(true);
    }
  });
  it('rejects malformed expressions', () => {
    for (const bad of [
      '',
      '0 8 * *',
      '60 8 * * *',
      '0 25 * * *',
      '0 8 * * 8-9',
      'foo bar * * *',
      '0 8 * * * *',
    ]) {
      expect(validateCron(bad), bad).toBe(false);
    }
  });
});

describe('validateTimezone', () => {
  it('accepts IANA names and rejects junk', () => {
    expect(validateTimezone('Europe/Paris')).toBe(true);
    expect(validateTimezone('America/New_York')).toBe(true);
    expect(validateTimezone('Mars/Olympus')).toBe(false);
    expect(validateTimezone('')).toBe(false);
  });
});

describe('nextOccurrence', () => {
  // 2026-07-06 is a Monday; 10:00 Paris = 08:00 UTC (CEST).
  const monday10Paris = new Date('2026-07-06T08:00:00Z');

  it('finds the next downtime edge in the schedule timezone', () => {
    const next = nextOccurrence(['0 22 * * *'], 'Europe/Paris', monday10Paris);
    expect(next?.toISOString()).toBe('2026-07-06T20:00:00.000Z'); // 22:00 Paris
  });

  it('rolls to the next matching weekday', () => {
    // Weekday-only uptime, asked on Friday evening → Monday 08:00.
    const fridayNight = new Date('2026-07-10T21:00:00Z');
    const next = nextOccurrence(['0 8 * * 1-5'], 'Europe/Paris', fridayNight);
    expect(next?.toISOString()).toBe('2026-07-13T06:00:00.000Z'); // Mon 08:00 Paris
  });

  it('honors non-UTC offsets', () => {
    // 18:00 New York = 22:00 UTC in July (EDT).
    const next = nextOccurrence(['0 18 * * *'], 'America/New_York', monday10Paris);
    expect(next?.toISOString()).toBe('2026-07-06T22:00:00.000Z');
  });

  it('picks the earliest among several expressions', () => {
    const next = nextOccurrence(['0 22 * * *', '0 12 * * *'], 'Europe/Paris', monday10Paris);
    expect(next?.toISOString()).toBe('2026-07-06T10:00:00.000Z'); // 12:00 Paris
  });

  it('returns null on invalid input', () => {
    expect(nextOccurrence(['not a cron'], 'Europe/Paris', monday10Paris)).toBeNull();
    expect(nextOccurrence(['0 8 * * *'], 'Mars/Olympus', monday10Paris)).toBeNull();
    expect(nextOccurrence([], 'Europe/Paris', monday10Paris)).toBeNull();
  });
});
