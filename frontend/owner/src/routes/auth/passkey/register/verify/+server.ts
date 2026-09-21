import { json } from '@sveltejs/kit';
import type { RequestHandler } from './$types';
import { api } from '$lib/grpc/clients';

export const POST: RequestHandler = async ({ request, locals }) => {
  let body: { friendlyName?: string; attestation?: unknown } = {};
  try {
    body = await request.json();
  } catch {
    return json({ error: 'invalid request' }, { status: 400 });
  }

  if (!body.attestation) {
    return json({ error: 'an attestation is required' }, { status: 400 });
  }

  try {
    await api.ownerAuth.finishPasskeyRegistration(
      locals.accessToken!,
      new TextEncoder().encode(JSON.stringify(body.attestation)),
      (body.friendlyName ?? '').trim(),
    );
    return json({ ok: true });
  } catch {
    return json({ error: 'passkey enrolment failed' }, { status: 400 });
  }
};
