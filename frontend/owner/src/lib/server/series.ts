import type { Series } from '@fijo/api-client';
import { studioContext } from '$lib/server/studio';
import { centsOf } from '$lib/schedule';
import { isDate, minuteOf } from '$lib/time';

/** A standing slot as the pages draw it and SeriesFields edits it. */
export function shapeSeries(s: Series, today: string) {
  return {
    id: s.id,
    offeringId: s.offeringId,
    weekday: s.weekday,
    startMinute: s.startMinute,
    intervalWeeks: s.intervalWeeks,
    startsOn: s.startsOn,
    endsOn: s.endsOn,
    priceOverride: s.priceOverride ?? null,
    staffUserIds: s.staffUserIds,
    attendeeIds: s.attendeeIds,
    ended: s.endsOn !== '' && s.endsOn < today,
  };
}

export type ShapedSeries = ReturnType<typeof shapeSeries>;

/** Standing slots in the order a week runs. */
export function byWeekTime(a: ShapedSeries, b: ShapedSeries): number {
  return a.weekday - b.weekday || a.startMinute - b.startMinute;
}

/**
 * The slot a SeriesFields form describes, or what is wrong with it. Who it is for comes from the
 * form's attendees unless the caller already knows.
 */
export async function seriesOf(form: FormData, token: string, attendees?: string[]): Promise<Series | string> {
  const text = (name: string) => String(form.get(name) ?? '').trim();
  const attendeeIds = [...new Set((attendees ?? form.getAll('attendees').map(String)).map((v) => v.trim()))].filter(
    Boolean,
  );
  if (attendeeIds.length === 0) return 'Choose who the lesson is for.';
  const weekday = Number(text('weekday'));
  if (!Number.isInteger(weekday) || weekday < 0 || weekday > 6) return 'Choose a day of the week.';
  const startMinute = minuteOf(text('time'));
  if (Number.isNaN(startMinute)) return 'Give a start time.';
  const startsOn = text('startsOn');
  if (!isDate(startsOn)) return 'Give the first date this lesson runs.';
  const endsOn = text('endsOn');
  if (endsOn && !isDate(endsOn)) return 'The end date is not a date.';
  const intervalWeeks = text('intervalWeeks') === '2' ? 2 : 1;
  let priceOverride: number | undefined;
  if (text('priceOverride')) {
    priceOverride = centsOf(text('priceOverride'));
    if (Number.isNaN(priceOverride)) return 'The price is an amount like 35 or 35.50.';
  }
  let staffUserIds = text('staff')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  if (staffUserIds.length === 0) {
    const { userId } = await studioContext(token);
    staffUserIds = userId ? [userId] : [];
  }
  return {
    id: text('id'),
    offeringId: text('offeringId'),
    weekday,
    startMinute,
    intervalWeeks,
    startsOn,
    endsOn,
    priceOverride,
    staffUserIds,
    attendeeIds,
    archived: false,
  };
}
