import { redirect, type Handle } from '@sveltejs/kit';
import { api } from '$lib/grpc/clients';
import { isUnauthenticated } from '$lib/errors';
import { clearSession, readSession, writeSession } from '$lib/auth/session';

const LOGIN_PATH = '/login';

// Paths a visitor reaches before they have a session.
const PUBLIC_PATHS = ['/login', '/logout', '/forgot-password', '/reset-password', '/auth/passkey/login', '/_ops_'];

function isPublic(pathname: string): boolean {
  return PUBLIC_PATHS.some((p) => pathname === p || pathname.startsWith(`${p}/`));
}

// Every other request has to carry a live session. The token is checked against the API by
// asking it who the caller is; a refused token is exchanged once through the refresh token,
// and a refused refresh sends the visitor back to sign in.
export const handle: Handle = async ({ event, resolve }) => {
  if (isPublic(event.url.pathname)) {
    return resolve(event);
  }

  const session = readSession(event.cookies);
  if (!session) {
    clearSession(event.cookies);
    throw redirect(302, LOGIN_PATH);
  }

  try {
    await api.owners.getSelf(session.accessToken);
    event.locals.accessToken = session.accessToken;
  } catch (err) {
    if (!isUnauthenticated(err) || !session.refreshToken) {
      clearSession(event.cookies);
      throw redirect(302, LOGIN_PATH);
    }

    try {
      const refreshed = await api.signIn.exchangeRefreshToken(session.refreshToken);
      if (!refreshed.token?.token) throw new Error('no token in refresh response');
      writeSession(event.cookies, { accessToken: refreshed.token.token, refreshToken: refreshed.token.refreshToken });
      event.locals.accessToken = refreshed.token.token;
    } catch {
      clearSession(event.cookies);
      throw redirect(302, LOGIN_PATH);
    }
  }

  return resolve(event);
};
