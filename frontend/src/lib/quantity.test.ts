import { describe, expect, it } from 'vitest';
import {
  displayCpu,
  displayMemory,
  formatCpu,
  formatMemory,
  parseCpu,
  parseMemory,
} from './quantity';

describe('cpu quantities', () => {
  it('parses millicores and whole cores', () => {
    expect(parseCpu('500m')).toBe(500);
    expect(parseCpu('2')).toBe(2000);
    expect(parseCpu(' 1.5 ')).toBe(1500);
  });

  it('formats back to the tightest form', () => {
    expect(formatCpu(2000)).toBe('2');
    expect(formatCpu(1500)).toBe('1500m');
  });

  it('displays in cores', () => {
    expect(displayCpu(1500)).toBe('1.5');
  });
});

describe('memory quantities', () => {
  it('parses binary suffixes, decimal suffixes and bare bytes', () => {
    expect(parseMemory('512Mi')).toBe(512 * 1024 ** 2);
    expect(parseMemory('2Gi')).toBe(2 * 1024 ** 3);
    expect(parseMemory('1K')).toBe(1000);
    expect(parseMemory('1073741824')).toBe(1024 ** 3);
  });

  it('rejects garbage and unknown units as NaN', () => {
    expect(parseMemory('lots')).toBeNaN();
    expect(parseMemory('5Zi')).toBeNaN();
  });

  it('formats into the tightest binary suffix', () => {
    expect(formatMemory(2 * 1024 ** 3)).toBe('2Gi');
    expect(formatMemory(1536 * 1024 ** 2)).toBe('1536Mi');
    expect(formatMemory(1024 + 512)).toBe('2Ki');
  });

  it('displays in Gi with one decimal', () => {
    expect(displayMemory(1.5 * 1024 ** 3)).toBe('1.5 Gi');
    expect(displayMemory(1024 ** 3)).toBe('1 Gi');
  });
});
