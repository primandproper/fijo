import type { Household } from '@fijo/api-client';
import { api } from '$lib/grpc/clients';
import { displayPhone, languageName, relationshipName } from '$lib/roster';

/** Somebody who takes lessons, as a slot's picker lists them. */
export interface Student {
  id: string;
  name: string;
  householdId: string;
  household: string;
}

/** A household as the pages draw it. */
export function shapeHousehold(h: Household) {
  return {
    id: h.id,
    name: h.name,
    language: languageName(h.language),
    notes: h.notes,
    contacts: h.contacts.map((c) => ({
      id: c.id,
      name: c.name,
      phone: c.phone,
      displayPhone: displayPhone(c.phone),
      language: languageName(c.language),
      relationship: relationshipName(c.relationship),
      optedOut: c.optedOut,
    })),
    attendees: h.attendees.map((a) => ({
      id: a.id,
      name: a.name,
      language: languageName(a.language),
      contactId: a.contactId,
    })),
  };
}

export type ShapedHousehold = ReturnType<typeof shapeHousehold>;

/** Every live student in the studio, and their names by identifier. */
export async function loadStudents(token: string): Promise<{ students: Student[]; names: Record<string, string> }> {
  const res = await api.roster.listHouseholds(token, { includeArchived: false });
  const students = res.households
    .flatMap((h) => h.attendees.map((a) => ({ id: a.id, name: a.name, householdId: h.id, household: h.name })))
    .sort((a, b) => a.name.localeCompare(b.name));
  return { students, names: Object.fromEntries(students.map((s) => [s.id, s.name])) };
}
