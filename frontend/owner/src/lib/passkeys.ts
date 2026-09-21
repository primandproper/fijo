/**
 * The browser half of the WebAuthn ceremonies. The API speaks the W3C JSON shapes, so the options it
 * returns go straight to parseCreationOptionsFromJSON / parseRequestOptionsFromJSON, and the
 * credential the browser produces goes back through toJSON. Both are the standard's own.
 */

type Creation = PublicKeyCredential & { toJSON(): unknown };

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    credentials: 'include',
  });
  const payload = (await res.json()) as T & { error?: string };
  if (!res.ok) {
    throw new Error(payload.error ?? `request to ${path} failed`);
  }
  return payload;
}

export async function enrolPasskey(friendlyName: string): Promise<void> {
  const { options } = await postJSON<{ options: { publicKey: PublicKeyCredentialCreationOptionsJSON } }>(
    '/auth/passkey/register/options',
    {},
  );
  const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(options.publicKey);
  const credential = (await navigator.credentials.create({ publicKey })) as Creation | null;
  if (!credential) throw new Error('No credential was created');
  await postJSON('/auth/passkey/register/verify', { friendlyName, attestation: credential.toJSON() });
}

export async function signInWithPasskey(username: string): Promise<string> {
  const { options } = await postJSON<{ options: { publicKey: PublicKeyCredentialRequestOptionsJSON } }>(
    '/auth/passkey/login/options',
    { username },
  );
  const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(options.publicKey);
  const credential = (await navigator.credentials.get({ publicKey })) as Creation | null;
  if (!credential) throw new Error('No credential was presented');
  const result = await postJSON<{ redirect: string }>('/auth/passkey/login/verify', {
    username,
    assertion: credential.toJSON(),
  });
  return result.redirect;
}
