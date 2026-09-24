import { fail } from '@sveltejs/kit';
import type { Series } from '@fijo/api-client';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { studioContext } from '$lib/server/studio';
import { centsOf } from '$lib/schedule';
import { isDate, minuteOf, wallClock } from '$lib/time';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [ctx, offerings, series] = await Promise.all([
    studioContext(token),
    api.schedule.listOfferings(token, { includeArchived: true }),
    api.schedule.listSeries(token, { includeArchived: false }),
  ]);

  const today = wallClock(new Date(), ctx.studioZone).date;

  return {
    studioZone: ctx.studioZone,
    today,
    offerings: offerings.offerings
      .filter((o) => !o.archived)
      .map((o) => ({ id: o.id, name: o.nameEn, durationMinutes: o.durationMinutes })),
    offeringNames: Object.fromEntries(offerings.offerings.map((o) => [o.id, o.nameEn])),
    series: series.series
      .map((s) => ({
        id: s.id,
        offeringId: s.offeringId,
        weekday: s.weekday,
        startMinute: s.startMinute,
        intervalWeeks: s.intervalWeeks,
        startsOn: s.startsOn,
        endsOn: s.endsOn,
        priceOverride: s.priceOverride ?? null,
        staffUserIds: s.staffUserIds,
        ended: s.endsOn !== '' && s.endsOn < today,
      }))
      .sort((a, b) => a.weekday - b.weekday || a.startMinute - b.startMinute),
  };
};

async function seriesOf(form: FormData, token: string): Promise<Series | string> {
  const text = (name: string) => String(form.get(name) ?? '').trim();
  const weekday = Number(text('weekday'));
  if (!Number.isInteger(weekday) || weekday < 0 || weekday > 6) return 'Choose a day of the week.';
  const startMinute = minuteOf(text('time'));
  if (Number.isNaN(startMinute)) return 'Give a start time.';
  const startsOn = text('startsOn');
  if (!isDate(startsOn)) return 'Give the first date this slot runs.';
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
    archived: false,
  };
}

export const actions: Actions = {
  create: async ({ request, locals }) => {
    const token = locals.accessToken!;
    const series = await seriesOf(await request.formData(), token);
    if (typeof series === 'string') return fail(400, { error: series });
    try {
      await api.schedule.createSeries(token, { series });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That slot could not be saved.') });
    }
    return { saved: true };
  },
  update: async ({ request, locals }) => {
    const token = locals.accessToken!;
    const series = await seriesOf(await request.formData(), token);
    if (typeof series === 'string') return fail(400, { error: series });
    try {
      await api.schedule.updateSeries(token, { series });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That slot could not be saved.') });
    }
    return { saved: true };
  },
  end: async ({ request, locals }) => {
    const form = await request.formData();
    const endsOn = String(form.get('endsOn') ?? '').trim();
    if (!isDate(endsOn)) return fail(400, { error: 'Give the last date the slot runs.' });
    try {
      await api.schedule.endSeries(locals.accessToken!, {
        id: String(form.get('id') ?? ''),
        endsOn,
        reason: String(form.get('reason') ?? '').trim(),
      });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That slot could not be ended.') });
    }
    return { saved: true };
  },
};
