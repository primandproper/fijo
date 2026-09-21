/**
 * AES-256-GCM cookie encryption, keyed by COOKIE_ENCRYPTION_KEY: 32 bytes, base64.
 * Format: base64(iv || ciphertext || authTag).
 */
import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto';

const IV_LENGTH = 12;
const AUTH_TAG_LENGTH = 16;
const KEY_LENGTH = 32;

function key(encoded: string | undefined): Buffer {
  if (!encoded) {
    throw new Error('COOKIE_ENCRYPTION_KEY is required');
  }
  const decoded = Buffer.from(encoded, 'base64');
  if (decoded.length !== KEY_LENGTH) {
    throw new Error(`COOKIE_ENCRYPTION_KEY must decode to ${KEY_LENGTH} bytes`);
  }
  return decoded;
}

export function encrypt(value: unknown, encodedKey: string | undefined): string {
  const iv = randomBytes(IV_LENGTH);
  const cipher = createCipheriv('aes-256-gcm', key(encodedKey), iv, { authTagLength: AUTH_TAG_LENGTH });
  const plaintext = JSON.stringify(value);
  const encrypted = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final(), cipher.getAuthTag()]);
  return Buffer.concat([iv, encrypted]).toString('base64');
}

export function decrypt<T = unknown>(encoded: string, encodedKey: string | undefined): T {
  const buf = Buffer.from(encoded, 'base64');
  if (buf.length < IV_LENGTH + AUTH_TAG_LENGTH) {
    throw new Error('Invalid encrypted payload');
  }
  const iv = buf.subarray(0, IV_LENGTH);
  const ciphertext = buf.subarray(IV_LENGTH, buf.length - AUTH_TAG_LENGTH);
  const authTag = buf.subarray(buf.length - AUTH_TAG_LENGTH);
  const decipher = createDecipheriv('aes-256-gcm', key(encodedKey), iv, { authTagLength: AUTH_TAG_LENGTH });
  decipher.setAuthTag(authTag);
  const plaintext = decipher.update(ciphertext) + decipher.final('utf8');
  return JSON.parse(plaintext) as T;
}
