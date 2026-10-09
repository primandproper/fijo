import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { languageOf } from '$lib/roster';
import { shapeHousehold } from '$lib/server/roster';
import { seriesOf, shapeSeries } from '$lib/server/series';
import { studioContext } from '$lib/server/studio';
import { displayClock, wallClock, WEEKDAYS } from '$lib/time';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [ctx, households, series, offerings] = await Promise.all([
    studioContext(token),
    api.roster.listHouseholds(token, { includeArchived: false }),
    api.schedule.listSeries(token, { includeArchived: false }),
    api.schedule.listOfferings(token, { includeArchived: false }),
  ]);

  const today = wallClock(new Date(), ctx.studioZone).date;
  const live = series.series.map((s) => shapeSeries(s, today)).filter((s) => !s.ended);

  return {
    today,
    studioZone: ctx.studioZone,
    offerings: offerings.offerings.map((o) => ({ id: o.id, name: o.nameEn, durationMinutes: o.durationMinutes })),
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

/**
 * Which of a new client's students their first lesson is for: "contact" for the contact who
 * attends, "child:<n>" for the nth name typed, "all" for everybody taught together.
 */
function chosenStudents(
  lessonFor: string,
  attends: boolean,
  children: string[],
  attendees: { id: string; name: string; contactId: string }[],
): string[] {
  const contact = attendees.filter((a) => a.contactId !== '').map((a) => a.id);
  const named = (name: string) => attendees.filter((a) => a.contactId === '' && a.name === name).map((a) => a.id);
  if (lessonFor === 'all') return attendees.map((a) => a.id);
  if (lessonFor === 'contact' && attends) return contact;
  const index = /^child:(\d+)$/.exec(lessonFor);
  if (index) return named(children[Number(index[1])] ?? '').slice(0, 1);
  // Nothing chosen: the only student, if there is just the one.
  return attendees.length === 1 ? [attendees[0].id] : [];
}

export const actions: Actions = {
  create: async ({ request, locals }) => {
    const token = locals.accessToken!;
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
    const withLesson = form.get('withLesson') === 'on';
    const lessonFor = text('lessonFor');

    const values = { name: text('name'), contactName, phone, attends, children: text('children'), withLesson };
    if (!contactName) return fail(400, { error: 'Give the name of the person you text.', values });
    if (!phone) return fail(400, { error: 'Give a phone number to text.', values });
    if (!attends && children.length === 0) {
      return fail(400, { error: 'Name who takes lessons, or tick that the contact does.', values });
    }

    // The lesson is checked before anything is written, so a mistyped time does not leave a client
    // behind without the lesson they came with. Who it is for is filled in once they exist.
    if (withLesson) {
      const lesson = await seriesOf(form, token, ['pending']);
      if (typeof lesson === 'string') return fail(400, { error: lesson, values });
      if ((attends ? 1 : 0) + children.length > 1 && !lessonFor) {
        return fail(400, { error: 'Choose who the first lesson is for.', values });
      }
    }

    let household;
    try {
      const res = await api.roster.createHousehold(token, {
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
      household = res.household;
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That client could not be added.'), values });
    }
    const id = household?.id ?? '';

    if (withLesson && household) {
      const students = chosenStudents(lessonFor, attends, children, household.attendees);
      const lesson = await seriesOf(form, token, students);
      let problem = typeof lesson === 'string' ? lesson : '';
      if (typeof lesson !== 'string') {
        try {
          await api.schedule.createSeries(token, { series: lesson });
        } catch (err) {
          problem = messageOf(err, 'The lesson could not be saved.');
        }
      }
      // The client exists either way; their page says what went wrong with the lesson.
      if (problem) redirect(303, `/clients/${id}?lesson=${encodeURIComponent(problem)}`);
    }

    redirect(303, `/clients/${id}`);
  },
};
