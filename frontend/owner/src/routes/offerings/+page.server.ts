import { fail } from '@sveltejs/kit';
import type { Offering } from '@fijo/api-client';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { centsOf, KINDS, kindName, type KindName } from '$lib/schedule';

export const load: PageServerLoad = async ({ locals }) => {
  const res = await api.schedule.listOfferings(locals.accessToken!, { includeArchived: false });
  return {
    offerings: res.offerings.map((o) => ({
      id: o.id,
      nameEn: o.nameEn,
      nameEs: o.nameEs,
      kind: kindName(o.kind),
      durationMinutes: o.durationMinutes,
      listPrice: o.listPrice,
      lessonCount: o.lessonCount,
      expiresAfterDays: o.expiresAfterDays,
      shareable: o.shareable,
    })),
  };
};

function offeringOf(form: FormData): Offering | string {
  const text = (name: string) => String(form.get(name) ?? '').trim();
  const kind = text('kind') as KindName;
  if (!(kind in KINDS)) return 'Choose what kind of offering this is.';
  const durationMinutes = Number(text('durationMinutes'));
  if (!Number.isInteger(durationMinutes) || durationMinutes <= 0) return 'The length is a whole number of minutes.';
  const listPrice = centsOf(text('listPrice'));
  if (Number.isNaN(listPrice)) return 'The price is an amount like 35 or 35.50.';
  const isPackage = kind === 'package';
  const lessonCount = isPackage ? Number(text('lessonCount')) : 0;
  if (isPackage && (!Number.isInteger(lessonCount) || lessonCount <= 0)) return 'A package needs a number of lessons.';
  const expiresAfterDays = isPackage ? Number(text('expiresAfterDays') || '0') : 0;
  return {
    id: text('id'),
    nameEn: text('nameEn'),
    nameEs: text('nameEs') || text('nameEn'),
    kind: KINDS[kind].value,
    durationMinutes,
    listPrice,
    lessonCount,
    expiresAfterDays: Number.isInteger(expiresAfterDays) ? expiresAfterDays : 0,
    shareable: isPackage && form.get('shareable') === 'on',
    archived: false,
  };
}

export const actions: Actions = {
  create: async ({ request, locals }) => {
    const offering = offeringOf(await request.formData());
    if (typeof offering === 'string') return fail(400, { error: offering });
    try {
      await api.schedule.createOffering(locals.accessToken!, { offering });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That offering could not be saved.') });
    }
    return { saved: true };
  },
  update: async ({ request, locals }) => {
    const offering = offeringOf(await request.formData());
    if (typeof offering === 'string') return fail(400, { error: offering });
    try {
      await api.schedule.updateOffering(locals.accessToken!, { offering });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That offering could not be saved.') });
    }
    return { saved: true };
  },
  archive: async ({ request, locals }) => {
    const id = String((await request.formData()).get('id') ?? '');
    try {
      await api.schedule.archiveOffering(locals.accessToken!, { id });
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That offering could not be retired.') });
    }
    return { saved: true };
  },
};
