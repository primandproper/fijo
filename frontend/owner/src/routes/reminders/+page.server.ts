import { fail } from '@sveltejs/kit';
import { Language } from '@fijo/api-client';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { shapeReminder } from '$lib/server/reminders';
import { studioContext } from '$lib/server/studio';
import { displayClock, displayDate, wallClock } from '$lib/time';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [ctx, settings, list] = await Promise.all([
    studioContext(token),
    api.reminders.getSettings(token, {}),
    api.reminders.list(token, { householdId: '', limit: 100 }),
  ]);

  let onSince = '';
  if (settings.remindersOnSince) {
    const wall = wallClock(settings.remindersOnSince, ctx.studioZone);
    onSince = `${displayDate(wall.date)}, ${displayClock(wall.minute)}`;
  }

  return {
    studioName: ctx.studioName,
    remindersOn: settings.remindersOn,
    onSince,
    replyTo: settings.replyTo || settings.suggestedReplyTo,
    replyToSaved: settings.replyTo !== '',
    reminders: list.reminders.map((r) => shapeReminder(r, ctx.studioZone)),
  };
};

export const actions: Actions = {
  save: async ({ request, locals }) => {
    const form = await request.formData();
    const remindersOn = form.get('remindersOn') === 'on';
    const replyTo = String(form.get('replyTo') ?? '').trim();
    if (remindersOn && !replyTo) {
      return fail(400, { error: 'Give an email address for your clients to write back to.' });
    }
    try {
      await api.reminders.saveSettings(locals.accessToken!, { remindersOn, replyTo });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'Those settings could not be saved.') });
    }
    return { saved: remindersOn ? 'on' : 'off' };
  },
  test: async ({ request, locals }) => {
    const form = await request.formData();
    const language = form.get('language') === 'es' ? Language.LANGUAGE_ES : Language.LANGUAGE_EN;
    try {
      const res = await api.reminders.sendTest(locals.accessToken!, { language });
      return { testedTo: res.sentTo };
    } catch (err) {
      return fail(400, { error: messageOf(err, 'The test reminder could not be sent.') });
    }
  },
};
