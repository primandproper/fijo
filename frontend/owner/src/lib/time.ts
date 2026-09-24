/**
 * Civil dates and wall clocks in a named zone, without a date library.
 *
 * The API speaks two vocabularies: a civil date ("2026-03-10") or minutes since midnight for a
 * rule, and an instant for an appointment. The grid is drawn in a zone, so every conversion
 * between the two happens here, through Intl, which knows the zone's rules.
 */

export const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatter(zone: string): Intl.DateTimeFormat {
  let f = formatters.get(zone);
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      hourCycle: 'h23',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
    formatters.set(zone, f);
  }
  return f;
}

/** Whether Intl recognises a zone name. */
export function isZone(zone: string): boolean {
  try {
    formatter(zone);
    return true;
  } catch {
    return false;
  }
}

export interface WallClock {
  /** The civil date, YYYY-MM-DD. */
  date: string;
  /** Minutes since midnight. */
  minute: number;
}

/** What a clock on the wall in a zone reads at an instant. */
export function wallClock(instant: Date, zone: string): WallClock {
  const parts: Record<string, string> = {};
  for (const p of formatter(zone).formatToParts(instant)) parts[p.type] = p.value;
  return {
    date: `${parts.year}-${parts.month}-${parts.day}`,
    minute: Number(parts.hour) * 60 + Number(parts.minute),
  };
}

/** The zone's offset from UTC at an instant, in minutes. */
function offsetAt(instant: Date, zone: string): number {
  const wall = wallClock(instant, zone);
  const asUtc = Date.parse(`${wall.date}T00:00:00Z`) + wall.minute * 60_000;
  const truncated = Math.floor(instant.getTime() / 60_000) * 60_000;
  return (asUtc - truncated) / 60_000;
}

/**
 * The instant a wall clock in a zone names. A skipped wall clock resolves backwards by the gap,
 * as Go's time.Date does on the server, so a time an owner types lands where the materializer
 * would have put it.
 */
export function instantOf(date: string, minute: number, zone: string): Date {
  const naive = Date.parse(`${date}T00:00:00Z`) + minute * 60_000;
  const first = offsetAt(new Date(naive), zone);
  const guess = naive - first * 60_000;
  const second = offsetAt(new Date(guess), zone);
  return new Date(second === first ? guess : naive - second * 60_000);
}

/** A civil date moved by whole days. */
export function addDays(date: string, days: number): string {
  const t = Date.parse(`${date}T00:00:00Z`) + days * 86_400_000;
  return new Date(t).toISOString().slice(0, 10);
}

/** 0 for Sunday, as the API counts them. */
export function weekdayOf(date: string): number {
  return new Date(`${date}T00:00:00Z`).getUTCDay();
}

/** The Monday on or before a civil date. */
export function mondayOf(date: string): string {
  return addDays(date, -((weekdayOf(date) + 6) % 7));
}

export function isDate(value: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(Date.parse(`${value}T00:00:00Z`));
}

/** "16:05" from minutes since midnight. */
export function clockOf(minute: number): string {
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;
}

/** Minutes since midnight from "16:05", or NaN. */
export function minuteOf(clock: string): number {
  const m = /^(\d{1,2}):(\d{2})$/.exec(clock.trim());
  if (!m) return NaN;
  const hours = Number(m[1]);
  const minutes = Number(m[2]);
  if (hours > 23 || minutes > 59) return NaN;
  return hours * 60 + minutes;
}

/** "4:05 PM" from minutes since midnight. */
export function displayClock(minute: number): string {
  const h = Math.floor(minute / 60);
  const m = minute % 60;
  const suffix = h < 12 ? 'AM' : 'PM';
  const hour = h % 12 === 0 ? 12 : h % 12;
  return `${hour}:${String(m).padStart(2, '0')} ${suffix}`;
}

/** "Tue, Mar 10" from a civil date. */
export function displayDate(date: string): string {
  return new Date(`${date}T12:00:00Z`).toLocaleDateString('en-US', {
    timeZone: 'UTC',
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  });
}
