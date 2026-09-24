import { describe, expect, it } from 'vitest';
import { addDays, clockOf, instantOf, minuteOf, mondayOf, wallClock, weekdayOf } from './time';

describe('instantOf', () => {
  it('reads a wall clock in the zone', () => {
    // Four in the afternoon in Chicago in winter is ten at night UTC.
    expect(instantOf('2026-03-03', 16 * 60, 'America/Chicago').toISOString()).toBe('2026-03-03T22:00:00.000Z');
    // And in summer, nine.
    expect(instantOf('2026-03-10', 16 * 60, 'America/Chicago').toISOString()).toBe('2026-03-10T21:00:00.000Z');
  });

  it('round-trips through wallClock', () => {
    for (const zone of ['America/Chicago', 'Europe/Madrid', 'Asia/Kolkata', 'UTC']) {
      const instant = instantOf('2026-11-01', 9 * 60 + 30, zone);
      expect(wallClock(instant, zone)).toEqual({ date: '2026-11-01', minute: 9 * 60 + 30 });
    }
  });

  it('moves a skipped wall clock backwards, as Go does', () => {
    // Two-thirty on the morning the clocks go forward does not exist in Chicago.
    const wall = wallClock(instantOf('2026-03-08', 2 * 60 + 30, 'America/Chicago'), 'America/Chicago');
    expect(wall).toEqual({ date: '2026-03-08', minute: 1 * 60 + 30 });
  });
});

describe('civil dates', () => {
  it('counts weekdays as Go does', () => {
    expect(weekdayOf('2026-03-01')).toBe(0);
    expect(weekdayOf('2026-03-03')).toBe(2);
  });

  it('finds the Monday', () => {
    expect(mondayOf('2026-03-01')).toBe('2026-02-23');
    expect(mondayOf('2026-03-02')).toBe('2026-03-02');
    expect(mondayOf('2026-03-08')).toBe('2026-03-02');
  });

  it('adds days across a month', () => {
    expect(addDays('2026-02-27', 3)).toBe('2026-03-02');
  });
});

describe('clocks', () => {
  it('round-trips', () => {
    expect(clockOf(16 * 60 + 5)).toBe('16:05');
    expect(minuteOf('16:05')).toBe(16 * 60 + 5);
    expect(minuteOf('24:00')).toBeNaN();
  });
});
