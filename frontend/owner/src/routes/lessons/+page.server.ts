import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { loadStudents } from '$lib/server/roster';
import { byWeekTime, seriesOf, shapeSeries } from '$lib/server/series';
import { studioContext } from '$lib/server/studio';
import { isDate, wallClock } from '$lib/time';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [ctx, offerings, series, roster] = await Promise.all([
    studioContext(token),
    api.schedule.listOfferings(token, { includeArchived: true }),
    api.schedule.listSeries(token, { includeArchived: false }),
    loadStudents(token),
  ]);

  const today = wallClock(new Date(), ctx.studioZone).date;

  return {
    studioZone: ctx.studioZone,
    today,
    offerings: offerings.offerings
      .filter((o) => !o.archived)
      .map((o) => ({ id: o.id, name: o.nameEn, durationMinutes: o.durationMinutes })),
    offeringNames: Object.fromEntries(offerings.offerings.map((o) => [o.id, o.nameEn])),
    students: roster.students,
    studentNames: roster.names,
    series: series.series.map((s) => shapeSeries(s, today)).sort(byWeekTime),
  };
};

export const actions: Actions = {
  create: async ({ request, locals }) => {
    const token = locals.accessToken!;
    const series = await seriesOf(await request.formData(), token);
    if (typeof series === 'string') return fail(400, { error: series });
    try {
      await api.schedule.createSeries(token, { series });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That lesson could not be saved.') });
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
      return fail(400, { error: messageOf(err, 'That lesson could not be saved.') });
    }
    return { saved: true };
  },
  end: async ({ request, locals }) => {
    const form = await request.formData();
    const endsOn = String(form.get('endsOn') ?? '').trim();
    if (!isDate(endsOn)) return fail(400, { error: 'Give the last date the lesson runs.' });
    try {
      await api.schedule.endSeries(locals.accessToken!, {
        id: String(form.get('id') ?? ''),
        endsOn,
        reason: String(form.get('reason') ?? '').trim(),
      });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That lesson could not be ended.') });
    }
    return { saved: true };
  },
};
