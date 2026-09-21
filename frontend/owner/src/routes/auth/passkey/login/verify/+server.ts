import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { api } from '$lib/grpc/clients';
import { writeSession } from '$lib/auth/session';

export const POST: RequestHandler = async ({ request, cookies }) => {
  let body: { username?: string; assertion?: unknown } = {};
  try {
    body = await request.json();
  } catch {
    return json({ error: 'invalid request' }, { status: 400 });
  }

  if (!body.assertion) {
    return json({ error: 'an assertion is required' }, { status: 400 });
  }

  try {
    const res = await api.ownerAuth.finishPasskeyLogin(
      (body.username ?? '').trim(),
      new TextEncoder().encode(JSON.stringify(body.assertion)),
    );
    const token = res.token;
    if (!token?.token) {
      return json({ error: 'no token' }, { status: 500 });
    }
    writeSession(cookies, { accessToken: token.token, refreshToken: token.refreshToken });
    return json({ redirect: '/' });
  } catch {
    return json({ error: 'passkey sign-in failed' }, { status: 401 });
  }
};
