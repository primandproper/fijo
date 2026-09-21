import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { messageOf } from '$lib/errors';
import { readSession, writeSession } from '$lib/auth/session';

export const load: PageServerLoad = async ({ cookies, url }) => {
  if (readSession(cookies)) {
    throw redirect(302, '/');
  }
  return { reset: url.searchParams.get('reset') === 'done' };
};

export const actions: Actions = {
  login: async ({ request, cookies }) => {
    const form = await request.formData();
    const username = String(form.get('username') ?? '').trim();
    const password = String(form.get('password') ?? '');

    if (!username || !password) {
      return fail(400, { error: 'Username and password are both required.', username });
    }

    try {
      const response = await api.signIn.loginForToken(username, password);
      const token = response.token;
      if (!token?.token) {
        return fail(500, { error: 'The API returned no token.', username });
      }
      writeSession(cookies, { accessToken: token.token, refreshToken: token.refreshToken });
    } catch (err) {
      return fail(401, { error: messageOf(err, 'Sign-in failed.'), username });
    }

    throw redirect(302, '/');
  },
};
