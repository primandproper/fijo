import { fail, redirect } from '@sveltejs/kit';
import { Canceller, type Occurrence } from '@fijo/api-client';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { loadStudents, type Student } from '$lib/server/roster';
import { studioContext } from '$lib/server/studio';
import { cancellerName, COMMAND_LABELS, stateName } from '$lib/schedule';
import { addDays, displayDate, instantOf, isDate, minuteOf, mondayOf, wallClock } from '$lib/time';

export const load: PageServerLoad = async ({ locals, params }) => {
  const token = locals.accessToken!;
  const ctx = await studioContext(token);
  const zone = ctx.zone;

  const today = wallClock(new Date(), zone).date;
  const requested = params.week ?? '';
  const week = mondayOf(isDate(requested) ? requested : today);
  const from = instantOf(week, 0, zone);
  const until = instantOf(addDays(week, 7), 0, zone);

  const [occurrences, offerings, series, closures, roster] = await Promise.all([
    api.schedule.listOccurrences(token, { from, until }),
    api.schedule.listOfferings(token, { includeArchived: true }),
    api.schedule.listSeries(token, { includeArchived: false }),
    api.schedule.listClosures(token, {}),
    loadStudents(token),
  ]);

  const names = new Map(offerings.offerings.map((o) => [o.id, o.nameEn]));
  const students = new Map(roster.students.map((s) => [s.id, s]));

  const shaped = occurrences.occurrences.map((o) => shape(o, zone, names, students));

  const days = Array.from({ length: 7 }, (_, i) => {
    const date = addDays(week, i);
    const closure = closures.closures.find((c) => c.startsOn <= date && date <= c.endsOn);
    return {
      date,
      label: displayDate(date),
      isToday: date === today,
      closedFor: closure ? closure.reason || 'Closed' : null,
      occurrences: shaped.filter((o) => o.date === date),
    };
  });

  const selectedId = params.occurrence ?? '';
  const selected = shaped.find((o) => o.id === selectedId) ?? null;

  let history: { id: string; at: string; what: string; reason: string }[] = [];
  let historyError = '';
  if (selected) {
    try {
      const entries = await api.schedule.listHistory(token, {
        resourceType: 'schedule.occurrence',
        resourceId: selected.id,
        limit: 20,
      });
      history = entries.entries.map((e) => ({
        id: e.id,
        at: e.occurredAt ? e.occurredAt.toISOString() : '',
        what: COMMAND_LABELS[e.metadata.command ?? ''] ?? e.eventType,
        reason: e.metadata.reason ?? '',
      }));
    } catch (err) {
      historyError = messageOf(err, 'History is unavailable.');
    }
  }

  return {
    zone,
    studioZone: ctx.studioZone,
    week,
    today,
    previousWeek: addDays(week, -7),
    nextWeek: addDays(week, 7),
    days,
    selected,
    history,
    historyError,
    hasOfferings: offerings.offerings.some((o) => !o.archived),
    hasSeries: series.series.length > 0,
  };
};

function shape(o: Occurrence, zone: string, names: Map<string, string>, students: Map<string, Student>) {
  const start = wallClock(o.startsAt ?? new Date(0), zone);
  const end = wallClock(o.endsAt ?? new Date(0), zone);
  const who = o.attendeeIds.map((id) => {
    const student = students.get(id);
    return { id, name: student?.name ?? 'Former student', householdId: student?.householdId ?? '' };
  });
  return {
    id: o.id,
    offering: names.get(o.offeringId) ?? 'Lesson',
    who,
    whoLabel: who.map((w) => w.name).join(' & '),
    date: start.date,
    startMinute: start.minute,
    endMinute: end.date === start.date ? end.minute : 24 * 60,
    state: stateName(o.state),
    cancelledBy: cancellerName(o.cancelledBy),
    insideNotice: o.cancelledInsideNotice ?? false,
    reason: o.reason,
    oneOff: o.seriesId === '',
    movedTo: o.movedToOccurrenceId,
    replaces: o.replacesOccurrenceId,
  };
}

function field(form: FormData, name: string): string {
  return String(form.get(name) ?? '').trim();
}

async function command(run: () => Promise<unknown>, fallback: string) {
  try {
    await run();
  } catch (err) {
    return fail(400, { error: messageOf(err, fallback) });
  }
  return { saved: true };
}

/** The instant a date and a time typed into the page name, in the grid's zone. */
async function typedInstant(token: string, form: FormData): Promise<Date | null> {
  const date = field(form, 'date');
  const minute = minuteOf(field(form, 'time'));
  if (!isDate(date) || Number.isNaN(minute)) return null;
  const { zone } = await studioContext(token);
  return instantOf(date, minute, zone);
}

/** Back to the week the occurrence now sits in, with it selected. */
async function landOn(token: string, id: string, startsAt: Date): Promise<never> {
  const { zone } = await studioContext(token);
  const week = mondayOf(wallClock(startsAt, zone).date);
  throw redirect(303, `/week/${week}/${encodeURIComponent(id)}`);
}

export const actions: Actions = {
  complete: async ({ request, locals }) => {
    const form = await request.formData();
    return command(
      () =>
        api.schedule.completeOccurrence(locals.accessToken!, { id: field(form, 'id'), reason: field(form, 'reason') }),
      'That lesson could not be marked completed.',
    );
  },
  noShow: async ({ request, locals }) => {
    const form = await request.formData();
    return command(
      () =>
        api.schedule.noShowOccurrence(locals.accessToken!, { id: field(form, 'id'), reason: field(form, 'reason') }),
      'That lesson could not be marked a no-show.',
    );
  },
  revert: async ({ request, locals }) => {
    const form = await request.formData();
    return command(
      () =>
        api.schedule.revertOccurrence(locals.accessToken!, { id: field(form, 'id'), reason: field(form, 'reason') }),
      'That lesson could not be reverted.',
    );
  },
  cancel: async ({ request, locals }) => {
    const form = await request.formData();
    const cancelledBy = field(form, 'by') === 'studio' ? Canceller.CANCELLER_STUDIO : Canceller.CANCELLER_HOUSEHOLD;
    return command(
      () =>
        api.schedule.cancelOccurrence(locals.accessToken!, {
          id: field(form, 'id'),
          cancelledBy,
          reason: field(form, 'reason'),
        }),
      'That lesson could not be cancelled.',
    );
  },
  move: async ({ request, locals }) => {
    const token = locals.accessToken!;
    const form = await request.formData();
    const startsAt = await typedInstant(token, form);
    if (!startsAt) return fail(400, { error: 'Give a date and a time to move it to.' });
    let movedId = '';
    try {
      const res = await api.schedule.moveOccurrence(token, {
        id: field(form, 'id'),
        startsAt,
        reason: field(form, 'reason'),
      });
      movedId = res.occurrence?.id ?? '';
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That lesson could not be moved.') });
    }
    return landOn(token, movedId, startsAt);
  },
  makeUp: async ({ request, locals }) => {
    const token = locals.accessToken!;
    const form = await request.formData();
    const startsAt = await typedInstant(token, form);
    if (!startsAt) return fail(400, { error: 'Give a date and a time for the make-up.' });
    let makeUpId = '';
    try {
      const res = await api.schedule.scheduleMakeUp(token, {
        cancelledOccurrenceId: field(form, 'id'),
        startsAt,
        reason: field(form, 'reason'),
      });
      makeUpId = res.occurrence?.id ?? '';
    } catch (err) {
      return fail(400, { error: messageOf(err, 'The make-up could not be scheduled.') });
    }
    return landOn(token, makeUpId, startsAt);
  },
};
