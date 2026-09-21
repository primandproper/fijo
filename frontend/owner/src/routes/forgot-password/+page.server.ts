import { fail } from '@sveltejs/kit';
import type { Actions } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';

export const actions: Actions = {
  request: async ({ request }) => {
    const form = await request.formData();
    const emailAddress = String(form.get('emailAddress') ?? '').trim();
    if (!emailAddress) {
      return fail(400, { error: 'An email address is required.' });
    }
    try {
      await api.ownerAuth.requestPasswordReset(emailAddress);
    } catch (err) {
      return fail(500, { error: messageOf(err, 'The reset could not be requested.') });
    }
    return { sent: true };
  },
};
