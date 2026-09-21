import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';

export const load: PageServerLoad = async ({ url }) => {
  return { token: url.searchParams.get('token') ?? '' };
};

export const actions: Actions = {
  reset: async ({ request }) => {
    const form = await request.formData();
    const token = String(form.get('token') ?? '').trim();
    const password = String(form.get('password') ?? '');
    const confirm = String(form.get('confirm') ?? '');

    if (!token) return fail(400, { error: 'This link is missing its token.' });
    if (password.length < 12) return fail(400, { error: 'Use at least 12 characters.' });
    if (password !== confirm) return fail(400, { error: 'The two passwords differ.' });

    try {
      await api.ownerAuth.completePasswordReset(token, password);
    } catch (err) {
      return fail(400, { error: messageOf(err, 'This link is no longer valid.') });
    }

    throw redirect(302, '/login?reset=done');
  },
};
