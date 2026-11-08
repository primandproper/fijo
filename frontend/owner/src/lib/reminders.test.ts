import { describe, expect, it } from 'vitest';
import { isReminderMinutes, offsetName } from './reminders';

describe('offsetName', () => {
  it('reads the usual choices', () => {
    expect(offsetName(1440)).toBe('a day before');
    expect(offsetName(60)).toBe('an hour before');
  });

  it('reads one set some other way', () => {
    expect(offsetName(90)).toBe('90 minutes before');
    expect(offsetName(300)).toBe('5 hours before');
    expect(offsetName(5760)).toBe('4 days before');
  });
});

describe('isReminderMinutes', () => {
  it('takes none, and fifteen minutes to a week', () => {
    expect(isReminderMinutes(0)).toBe(true);
    expect(isReminderMinutes(15)).toBe(true);
    expect(isReminderMinutes(10080)).toBe(true);
    expect(isReminderMinutes(5)).toBe(false);
    expect(isReminderMinutes(10081)).toBe(false);
    expect(isReminderMinutes(1.5)).toBe(false);
  });
});
