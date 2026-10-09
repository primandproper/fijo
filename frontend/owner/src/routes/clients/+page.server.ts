import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { languageOf } from '$lib/roster';
import { shapeHousehold } from '$lib/server/roster';
import { shapeSeries } from '$lib/server/series';
import { studioContext } from '$lib/server/studio';
import { displayClock, wallClock, WEEKDAYS } from '$lib/time';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [ctx, households, series] = await Promise.all([
    studioContext(token),
    api.roster.listHouseholds(token, { includeArchived: false }),
    api.schedule.listSeries(token, { includeArchived: false }),
  ]);

  const today = wallClock(new Date(), ctx.studioZone).date;
  const live = series.series.map((s) => shapeSeries(s, today)).filter((s) => !s.ended);

  return {
    households: households.households.map((h) => {
      const shaped = shapeHousehold(h);
      const ids = new Set(shaped.attendees.map((a) => a.id));
      const lessons = live
        .filter((s) => s.attendeeIds.some((id) => ids.has(id)))
        .map((s) => `${WEEKDAYS[s.weekday].slice(0, 3)} ${displayClock(s.startMinute)}`);
      return { ...shaped, lessons };
    }),
  };
};

export const actions: Actions = {
  create: async ({ request, locals }) => {
    const form = await request.formData();
    const text = (name: string) => String(form.get(name) ?? '').trim();
    const language = languageOf(text('language'));
    const contactName = text('contactName');
    const phone = text('phone');
    const attends = form.get('attends') === 'on';
    const children = text('children')
      .split('\n')
      .map((n) => n.trim())
      .filter(Boolean);
    const name = text('name') || contactName;

    const values = { name: text('name'), contactName, phone, attends, children: text('children') };
    if (!contactName) return fail(400, { error: 'Give the name of the person you text.', values });
    if (!phone) return fail(400, { error: 'Give a phone number to text.', values });
    if (!attends && children.length === 0) {
      return fail(400, { error: 'Name who takes lessons, or tick that the contact does.', values });
    }

    let id: string;
    try {
      const res = await api.roster.createHousehold(locals.accessToken!, {
        household: { id: '', name, language, notes: '', archived: false, contacts: [], attendees: [] },
        contacts: [
          {
            contact: {
              id: '',
              householdId: '',
              name: contactName,
              phone,
              language,
              relationship: 0,
              optedOut: false,
              verified: false,
            },
            attends,
          },
        ],
        attendees: children.map((child) => ({ id: '', householdId: '', name: child, language, contactId: '' })),
      });
      id = res.household?.id ?? '';
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That client could not be added.'), values });
    }
    redirect(303, `/clients/${id}`);
  },
};
