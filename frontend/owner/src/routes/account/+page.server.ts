import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';

export const load: PageServerLoad = async ({ locals }) => {
  const token = locals.accessToken!;
  const [self, passkeys] = await Promise.all([api.owners.getSelf(token), api.ownerAuth.listPasskeys(token)]);

  return {
    user: self.user,
    studio: self.studio,
    roles: self.roles,
    timeZone: self.timeZone,
    passkeys: passkeys.passkeys.map((p) => ({
      id: p.id,
      friendlyName: p.friendlyName || 'Passkey',
      createdAt: p.createdAt?.toISOString() ?? '',
      lastUsedAt: p.lastUsedAt?.toISOString() ?? '',
    })),
  };
};

export const actions: Actions = {
  setTimeZone: async ({ request, locals }) => {
    const form = await request.formData();
    const timeZone = String(form.get('timeZone') ?? '').trim();
    if (!timeZone) {
      return fail(400, { error: 'Choose a time zone.' });
    }
    try {
      await api.owners.setTimeZone(locals.accessToken!, timeZone);
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That time zone was not accepted.') });
    }
    return { saved: true };
  },
  archivePasskey: async ({ request, locals }) => {
    const form = await request.formData();
    const id = String(form.get('id') ?? '');
    if (!id) return fail(400, { error: 'Which passkey?' });
    try {
      await api.ownerAuth.archivePasskey(locals.accessToken!, id);
    } catch (err) {
      return fail(400, { error: messageOf(err, 'That passkey could not be removed.') });
    }
    return { saved: true };
  },
};
