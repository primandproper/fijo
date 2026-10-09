import { describe, expect, it } from 'vitest';
import { displayPhone, languageName, languageOf } from './roster';

describe('displayPhone', () => {
  it('formats a North American number', () => {
    expect(displayPhone('+15125550100')).toBe('(512) 555-0100');
  });

  it('leaves anything else as stored', () => {
    expect(displayPhone('+525512345678')).toBe('+525512345678');
  });
});

describe('languages', () => {
  it('round-trips through the wire numbers', () => {
    expect(languageName(languageOf('es'))).toBe('es');
    expect(languageName(languageOf('en'))).toBe('en');
  });
});
