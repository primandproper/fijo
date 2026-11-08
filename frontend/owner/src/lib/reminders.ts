import { ReminderStatus } from '@fijo/api-client';

/** The reminder times a household can choose, in minutes before the lesson; zero is none. */
export const REMINDER_CHOICES: { minutes: number; label: string }[] = [
  { minutes: 0, label: 'None' },
  { minutes: 15, label: '15 minutes before' },
  { minutes: 30, label: '30 minutes before' },
  { minutes: 60, label: 'An hour before' },
  { minutes: 120, label: 'Two hours before' },
  { minutes: 180, label: 'Three hours before' },
  { minutes: 360, label: 'Six hours before' },
  { minutes: 720, label: '12 hours before' },
  { minutes: 1440, label: 'A day before' },
  { minutes: 2880, label: 'Two days before' },
  { minutes: 4320, label: 'Three days before' },
  { minutes: 10080, label: 'A week before' },
];

/** How a reminder time reads: "a day before", or "90 minutes before" for one set some other way. */
export function offsetName(minutes: number): string {
  const choice = REMINDER_CHOICES.find((c) => c.minutes === minutes);
  if (choice) return choice.label.toLowerCase();
  if (minutes % 1440 === 0) return `${minutes / 1440} days before`;
  if (minutes % 60 === 0) return `${minutes / 60} hours before`;
  return `${minutes} minutes before`;
}

export type StatusName = 'waiting' | 'sent' | 'skipped' | 'failed';

export function statusName(status: ReminderStatus): StatusName {
  switch (status) {
    case ReminderStatus.REMINDER_STATUS_SENT:
      return 'sent';
    case ReminderStatus.REMINDER_STATUS_SKIPPED:
      return 'skipped';
    case ReminderStatus.REMINDER_STATUS_FAILED:
      return 'failed';
    default:
      return 'waiting';
  }
}

/** Whether a reminder time is one a household may ask for. */
export function isReminderMinutes(value: number): boolean {
  return Number.isInteger(value) && (value === 0 || (value >= 15 && value <= 10080));
}
