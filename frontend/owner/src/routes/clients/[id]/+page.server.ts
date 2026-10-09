import { error, fail, redirect } from '@sveltejs/kit';
import { status } from '@grpc/grpc-js';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { languageOf, RELATIONSHIPS } from '$lib/roster';
import { shapeHousehold } from '$lib/server/roster';
import { byWeekTime, seriesOf, shapeSeries } from '$lib/server/series';
import { studioContext } from '$lib/server/studio';
import { wallClock } from '$lib/time';

export const load: PageServerLoad = async ({ locals, params }) => {
  const token = locals.accessToken!;

  let household;
  try {
    household = (await api.roster.getHousehold(token, { id: params.id })).household;
  } catch (err) {
    if ((err as { code?: number }).code === status.NOT_FOUND) error(404, 'No such client.');
    throw err;
  }
  if (!household) error(404, 'No such client.');

  const [ctx, offerings, series, roster] = await Promise.all([
    studioContext(token),
    api.schedule.listOfferings(token, { includeArchived: true }),
    api.schedule.listSeries(token, { includeArchived: false }),
    api.roster.listHouseholds(token, { includeArchived: false }),
  ]);

  const today = wallClock(new Date(), ctx.studioZone).date;
  const shaped = shapeHousehold(household);
  const mine = new Set(shaped.attendees.map((a) => a.id));
  const names = Object.fromEntries(roster.households.flatMap((h) => h.attendees.map((a) => [a.id, a.name])));

  return {
    studioZone: ctx.studioZone,
    today,
    household: shaped,
    offerings: offerings.offerings
      .filter((o) => !o.archived)
      .map((o) => ({ id: o.id, name: o.nameEn, durationMinutes: o.durationMinutes })),
    offeringNames: Object.fromEntries(offerings.offerings.map((o) => [o.id, o.nameEn])),
    studentNames: names,
    lessons: series.series
      .map((s) => shapeSeries(s, today))
      .filter((s) => !s.ended && s.attendeeIds.some((id) => mine.has(id)))
      .sort(byWeekTime),
  };
};

function text(form: FormData, name: string): string {
  return String(form.get(name) ?? '').trim();
}

async function attempt(run: () => Promise<unknown>, fallback: string) {
  try {
    await run();
  } catch (err) {
    return fail(400, { error: messageOf(err, fallback) });
  }
  return { saved: true };
}

export const actions: Actions = {
  updateHousehold: async ({ request, locals, params }) => {
    const form = await request.formData();
    if (!text(form, 'name')) return fail(400, { error: 'A client needs a name.' });
    return attempt(
      () =>
        api.roster.updateHousehold(locals.accessToken!, {
          household: {
            id: params.id,
            name: text(form, 'name'),
            language: languageOf(text(form, 'language')),
            notes: text(form, 'notes'),
            archived: false,
            contacts: [],
            attendees: [],
          },
        }),
      'That client could not be saved.',
    );
  },
  archiveHousehold: async ({ locals, params }) => {
    try {
      await api.roster.archiveHousehold(locals.accessToken!, { id: params.id });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That client could not be archived.') });
    }
    redirect(303, '/clients');
  },
  saveContact: async ({ request, locals, params }) => {
    const form = await request.formData();
    const id = text(form, 'id');
    const contact = {
      id,
      householdId: params.id,
      name: text(form, 'name'),
      phone: text(form, 'phone'),
      language: languageOf(text(form, 'language')),
      relationship: RELATIONSHIPS[text(form, 'relationship') === 'self' ? 'self' : 'guardian'].value,
      optedOut: false,
      verified: false,
    };
    if (!contact.name) return fail(400, { error: 'A contact needs a name.' });
    if (!contact.phone) return fail(400, { error: 'A contact needs a phone number.' });
    const token = locals.accessToken!;
    return attempt(
      () =>
        id
          ? api.roster.updateContact(token, { contact })
          : api.roster.createContact(token, { contact, attends: form.get('attends') === 'on' }),
      'That contact could not be saved.',
    );
  },
  removeContact: async ({ request, locals }) => {
    const form = await request.formData();
    return attempt(
      () => api.roster.archiveContact(locals.accessToken!, { id: text(form, 'id') }),
      'That contact could not be removed.',
    );
  },
  saveStudent: async ({ request, locals, params }) => {
    const form = await request.formData();
    const id = text(form, 'id');
    const attendee = {
      id,
      householdId: params.id,
      name: text(form, 'name'),
      language: languageOf(text(form, 'language')),
      contactId: text(form, 'contactId'),
    };
    if (!attendee.name) return fail(400, { error: 'A student needs a name.' });
    const token = locals.accessToken!;
    return attempt(
      () => (id ? api.roster.updateAttendee(token, { attendee }) : api.roster.createAttendee(token, { attendee })),
      'That student could not be saved.',
    );
  },
  removeStudent: async ({ request, locals }) => {
    const form = await request.formData();
    return attempt(
      () => api.roster.archiveAttendee(locals.accessToken!, { id: text(form, 'id') }),
      'That student could not be removed.',
    );
  },
  addLesson: async ({ request, locals }) => {
    const token = locals.accessToken!;
    const series = await seriesOf(await request.formData(), token);
    if (typeof series === 'string') return fail(400, { error: series });
    return attempt(() => api.schedule.createSeries(token, { series }), 'That lesson could not be saved.');
  },
};
