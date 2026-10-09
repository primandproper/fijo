// Type-only, like $lib/schedule: shared with the browser. The enums' values are the proto's wire numbers.
import type { Language, Relationship } from '@fijo/api-client';

export type LanguageName = 'en' | 'es';

export const LANGUAGES: Record<LanguageName, { label: string; value: Language }> = {
  es: { label: 'Spanish', value: 2 as Language },
  en: { label: 'English', value: 1 as Language },
};

export function languageName(language: Language): LanguageName {
  return language === 2 ? 'es' : 'en';
}

export function languageOf(value: string): Language {
  return value === 'es' ? LANGUAGES.es.value : LANGUAGES.en.value;
}

export type RelationshipName = 'guardian' | 'self';

export function relationshipName(relationship: Relationship): RelationshipName {
  return relationship === 2 ? 'self' : 'guardian';
}

export const RELATIONSHIPS: Record<RelationshipName, { label: string; value: Relationship }> = {
  guardian: { label: 'Parent or guardian', value: 1 as Relationship },
  self: { label: 'Takes lessons', value: 2 as Relationship },
};

/** "(512) 555-0100" for a North American number, and the number as stored for anything else. */
export function displayPhone(e164: string): string {
  const match = /^\+1(\d{3})(\d{3})(\d{4})$/.exec(e164);
  return match ? `(${match[1]}) ${match[2]}-${match[3]}` : e164;
}
