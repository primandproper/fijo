import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { studioContext } from '$lib/server/studio';
import { isDate, wallClock } from '$lib/time';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [ctx, settings, closures] = await Promise.all([
    studioContext(token),
    api.schedule.getSettings(token, {}),
    api.schedule.listClosures(token, {}),
  ]);
  const today = wallClock(new Date(), ctx.studioZone).date;
  return {
    studioName: ctx.studioName,
    today,
    noticeHours: Math.round((settings.settings?.noticeWindowMinutes ?? 0) / 60),
    horizonWeeks: settings.settings?.horizonWeeks ?? 0,
    closures: closures.closures
      .map((c) => ({ id: c.id, startsOn: c.startsOn, endsOn: c.endsOn, reason: c.reason, past: c.endsOn < today }))
      .sort((a, b) => a.startsOn.localeCompare(b.startsOn)),
  };
};

export const actions: Actions = {
  settings: async ({ request, locals }) => {
    const form = await request.formData();
    const noticeHours = Number(form.get('noticeHours'));
    const horizonWeeks = Number(form.get('horizonWeeks'));
    if (!Number.isInteger(noticeHours) || noticeHours < 0)
      return fail(400, { error: 'Notice is a whole number of hours.' });
    if (!Number.isInteger(horizonWeeks) || horizonWeeks < 1 || horizonWeeks > 52)
      return fail(400, { error: 'Plan ahead between 1 and 52 weeks.' });
    try {
      await api.schedule.setSettings(locals.accessToken!, {
        settings: { noticeWindowMinutes: noticeHours * 60, horizonWeeks },
      });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'Those settings could not be saved.') });
    }
    return { saved: 'settings' };
  },
  close: async ({ request, locals }) => {
    const form = await request.formData();
    const startsOn = String(form.get('startsOn') ?? '');
    const endsOn = String(form.get('endsOn') ?? '') || startsOn;
    if (!isDate(startsOn) || !isDate(endsOn)) return fail(400, { error: 'Give the dates the studio is closed.' });
    if (endsOn < startsOn) return fail(400, { error: 'The closure ends before it starts.' });
    try {
      await api.schedule.closeStudio(locals.accessToken!, {
        startsOn,
        endsOn,
        reason: String(form.get('reason') ?? '').trim(),
      });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'The studio could not be closed for those dates.') });
    }
    return { saved: 'closure' };
  },
  reopen: async ({ request, locals }) => {
    const id = String((await request.formData()).get('id') ?? '');
    try {
      await api.schedule.reopenStudio(locals.accessToken!, { id });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That closure could not be removed.') });
    }
    return { saved: 'reopen' };
  },
};
