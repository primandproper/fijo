import type { Reminder } from '@fijo/api-client';
import { languageName } from '$lib/roster';
import { offsetName, statusName } from '$lib/reminders';
import { displayClock, displayDate, wallClock } from '$lib/time';

function when(instant: Date | undefined, zone: string): string {
  if (!instant) return '';
  const wall = wallClock(instant, zone);
  return `${displayDate(wall.date)}, ${displayClock(wall.minute)}`;
}

/** A reminder as the pages draw it, its times in the studio's zone. */
export function shapeReminder(r: Reminder, zone: string) {
  return {
    id: r.id,
    householdId: r.householdId,
    householdName: r.householdName,
    contactName: r.contactName,
    toAddress: r.toAddress,
    language: languageName(r.language),
    subject: r.subject,
    bodyHtml: r.bodyHtml,
    status: statusName(r.status),
    reason: r.reason,
    which: offsetName(r.offsetMinutes),
    lesson: when(r.lessonStartsAt, zone),
    written: when(r.createdAt, zone),
    sent: when(r.sentAt, zone),
  };
}

export type ShapedReminder = ReturnType<typeof shapeReminder>;
