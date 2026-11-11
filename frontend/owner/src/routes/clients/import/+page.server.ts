import { fail } from '@sveltejs/kit';
import type { ImportPreview } from '@fijo/api-client';
import type { Actions } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { languageOf } from '$lib/roster';

// Nothing is kept between the preview and the import: the preview hands the files' text back to the
// page, which posts it again with what the owner unticked. The keys in a preview come out the same
// from the same files, which is what lets a skipped key mean the same thing on the second post.

/** A file input's text, or the textarea that carries it from an earlier preview. */
async function textOf(form: FormData, file: string, carried: string): Promise<string> {
  const upload = form.get(file);
  if (upload instanceof File && upload.size > 0) return upload.text();
  return String(form.get(carried) ?? '');
}

function filesOf(form: FormData, clientsCsv: string, appointmentsCsv: string) {
  const language = String(form.get('language') ?? 'en') === 'es' ? 'es' : 'en';
  return {
    clientsCsv,
    appointmentsCsv,
    language,
    wire: { clientsCsv, appointmentsCsv, language: languageOf(language) },
  };
}

/**
 * The preview as the page draws it: plain data, with each lesson's offering resolved, and each
 * household carrying its lessons and one-offs. A client already here carries only what is new.
 */
function shapePreview(p: ImportPreview) {
  const offerings = Object.fromEntries(p.offerings.map((o) => [o.key, o]));
  return {
    columns: p.columns,
    warnings: p.warnings,
    offerings: p.offerings.map((o) => ({
      key: o.key,
      name: o.name,
      durationMinutes: o.durationMinutes,
      listPrice: Number(o.listPrice),
      existing: o.existingOfferingId !== '',
    })),
    households: p.households.map((h) => ({
      key: h.key,
      name: h.name,
      notes: h.notes,
      active: h.active,
      onRoster: h.onRoster,
      existingName: h.existingHouseholdName,
      warnings: h.warnings,
      contacts: h.contacts.map((c) => ({
        name: c.name,
        email: c.email,
        phone: c.phone,
        self: c.relationship === 2,
        guessedName: c.guessedName,
        onRoster: c.onRoster,
      })),
      attendees: h.attendees.map((a) => ({ name: a.name, onRoster: a.onRoster })),
      lessons: p.lessons
        .filter((l) => l.householdKey === h.key && !l.onRoster)
        .map((l) => ({
          key: l.key,
          who: l.attendeeName,
          offeringKey: l.offeringKey,
          offering: offerings[l.offeringKey]?.name ?? '',
          weekday: l.weekday,
          startMinutes: l.startMinutes,
          intervalWeeks: l.intervalWeeks,
          startsOn: l.startsOn,
          price: Number(l.price),
          seen: l.seen,
          warnings: l.warnings,
        })),
      appointments: p.appointments
        .filter((a) => a.householdKey === h.key && !a.onRoster)
        .map((a) => ({
          key: a.key,
          who: a.attendeeName,
          offeringKey: a.offeringKey,
          offering: offerings[a.offeringKey]?.name ?? '',
          on: a.on,
          startMinutes: a.startMinutes,
          durationMinutes: a.durationMinutes || offerings[a.offeringKey]?.durationMinutes || 0,
          reason: a.reason,
        })),
    })),
  };
}

export type ShapedPreview = ReturnType<typeof shapePreview>;

export const actions: Actions = {
  preview: async ({ request, locals }) => {
    const form = await request.formData();
    const files = filesOf(
      form,
      await textOf(form, 'clients', 'clientsCsv'),
      await textOf(form, 'appointments', 'appointmentsCsv'),
    );
    if (!files.clientsCsv.trim() && !files.appointmentsCsv.trim()) {
      return fail(400, { error: 'Choose the client list, the appointments report, or both.' });
    }

    try {
      const preview = await api.imports.previewAcuity(locals.accessToken!, { files: files.wire });
      return {
        preview: shapePreview(preview),
        files: { clientsCsv: files.clientsCsv, appointmentsCsv: files.appointmentsCsv, language: files.language },
      };
    } catch (err) {
      return fail(400, { error: messageOf(err, 'Those files could not be read.') });
    }
  },

  apply: async ({ request, locals }) => {
    const form = await request.formData();
    const files = filesOf(form, String(form.get('clientsCsv') ?? ''), String(form.get('appointmentsCsv') ?? ''));
    const kept = (name: string) => new Set(form.getAll(name).map(String));
    const shown = (name: string) => form.getAll(name).map(String);
    const households = kept('household');
    const lessons = kept('lesson');
    const appointments = kept('appointment');

    try {
      const result = await api.imports.applyAcuity(locals.accessToken!, {
        files: files.wire,
        skipHouseholdKeys: shown('householdShown').filter((key) => !households.has(key)),
        skipLessonKeys: shown('lessonShown').filter((key) => !lessons.has(key)),
        skipAppointmentKeys: shown('appointmentShown').filter((key) => !appointments.has(key)),
        // Which inactive clients come along is decided by their own boxes, so the switch is always on.
        includeInactive: true,
      });
      return { result };
    } catch (err) {
      return fail(400, { error: messageOf(err, 'The import did not go through. Nothing was added.') });
    }
  },
};
