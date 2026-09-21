import { describe, expect, it } from 'vitest';
import { decrypt, encrypt } from './crypto';

const KEY = Buffer.alloc(32, 7).toString('base64');

describe('cookie encryption', () => {
  it('round-trips a payload', () => {
    const payload = { accessToken: 'a', refreshToken: 'r' };
    expect(decrypt(encrypt(payload, KEY), KEY)).toEqual(payload);
  });

  it('refuses a tampered payload', () => {
    const encoded = Buffer.from(encrypt({ accessToken: 'a' }, KEY), 'base64');
    encoded[encoded.length - 1] ^= 0xff;
    expect(() => decrypt(encoded.toString('base64'), KEY)).toThrow();
  });

  it('refuses a short key', () => {
    expect(() => encrypt({}, Buffer.alloc(8).toString('base64'))).toThrow(/32 bytes/);
  });
});
