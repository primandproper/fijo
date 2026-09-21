import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { api } from '$lib/grpc/clients';

export const POST: RequestHandler = async ({ request }) => {
  let body: { username?: string } = {};
  try {
    body = await request.json();
  } catch {
    return json({ error: 'invalid request' }, { status: 400 });
  }

  try {
    const res = await api.ownerAuth.beginPasskeyLogin((body.username ?? '').trim());
    return json({ options: JSON.parse(Buffer.from(res.optionsJson).toString('utf8')) });
  } catch {
    return json({ error: 'passkey sign-in could not begin' }, { status: 401 });
  }
};
