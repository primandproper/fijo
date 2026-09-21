/**
 * The owner's session, as this server keeps it: the access token and refresh token the Go API
 * issued, encrypted into one HttpOnly cookie. The API keeps the revocable record; this is only the
 * bearer of it, the same shape DDB's consumer app uses.
 */
import { env } from '$env/dynamic/private';
import type { Cookies } from '@sveltejs/kit';
import { decrypt, encrypt } from './crypto';

export interface AuthPayload {
  accessToken: string;
  refreshToken?: string;
}

const COOKIE_LIFETIME_SECONDS = 30 * 24 * 60 * 60; // the refresh token's default lifetime

export function cookieName(): string {
  return env.COOKIE_NAME ?? 'fijo_owner_session';
}

export function encodeSession(payload: AuthPayload): string {
  return encrypt(payload, env.COOKIE_ENCRYPTION_KEY);
}

export function decodeSession(encoded: string): AuthPayload {
  return decrypt<AuthPayload>(encoded, env.COOKIE_ENCRYPTION_KEY);
}

export function writeSession(cookies: Cookies, payload: AuthPayload): void {
  cookies.set(cookieName(), encodeSession(payload), {
    path: '/',
    httpOnly: true,
    secure: env.NODE_ENV === 'production',
    sameSite: 'lax',
    maxAge: COOKIE_LIFETIME_SECONDS,
  });
}

export function clearSession(cookies: Cookies): void {
  cookies.delete(cookieName(), { path: '/' });
}

export function readSession(cookies: Cookies): AuthPayload | null {
  const value = cookies.get(cookieName());
  if (!value) return null;
  try {
    return decodeSession(value);
  } catch {
    return null;
  }
}
