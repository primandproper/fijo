import { describe, expect, it } from 'vitest';
import { zoneGroups, zoneName } from './zones';

const at = new Date('2026-01-15T12:00:00Z');

describe('zoneName', () => {
  it('names a common zone the way a person does', () => {
    expect(zoneName('America/Chicago')).toBe('Central Time');
    expect(zoneName('US/Central')).toBe('Central Time');
    expect(zoneName('America/Mexico_City')).toBe('Central Mexico');
  });

  it('names any other zone by its city', () => {
    expect(zoneName('Asia/Ho_Chi_Minh')).toBe('Ho Chi Minh');
  });
});

describe('zoneGroups', () => {
  it('leads with the common zones and their offsets', () => {
    const [us] = zoneGroups(at);
    expect(us.label).toBe('United States & Canada');
    expect(us.options.find((o) => o.value === 'America/Chicago')?.label).toContain('(GMT-6)');
  });

  it('lists every zone exactly once', () => {
    const values = zoneGroups(at).flatMap((g) => g.options.map((o) => o.value));
    expect(new Set(values).size).toBe(values.length);
    expect(values).toContain('Asia/Tokyo');
  });

  it('keeps a current value the lists spell differently', () => {
    const [first] = zoneGroups(at, 'US/Central');
    expect(first.label).toBe('Current');
    expect(first.options[0]).toEqual({ value: 'US/Central', label: 'Central Time (US/Central)' });
  });
});
