import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';
import { api } from '$lib/grpc/clients';
import { clearSession, readSession } from '$lib/auth/session';

// Ends the API session and the refresh family, then drops the cookie. A failure to reach the API
// still drops the cookie: the token expires on its own, and the person asked to leave.
export const load: PageServerLoad = async ({ cookies }) => {
  const session = readSession(cookies);
  if (session) {
    try {
      await api.ownerAuth.signOut(session.accessToken);
    } catch {
      // fall through
    }
  }
  clearSession(cookies);
  throw redirect(302, '/login');
};
