import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { api } from '$lib/grpc/clients';

export const POST: RequestHandler = async ({ locals }) => {
  try {
    const res = await api.ownerAuth.beginPasskeyRegistration(locals.accessToken!);
    return json({ options: JSON.parse(Buffer.from(res.optionsJson).toString('utf8')) });
  } catch {
    return json({ error: 'passkey enrolment could not begin' }, { status: 500 });
  }
};
