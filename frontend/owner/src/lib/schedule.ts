// Type-only: this module is shared with the browser, and the client package's runtime is grpc-js,
// which only runs on the server. The enums' values are the proto's wire numbers.
import type { Canceller, OccurrenceState, OfferingKind } from '@fijo/api-client';

/** The occurrence states as the page talks about them. */
export type StateName = 'scheduled' | 'completed' | 'cancelled' | 'no_show' | 'moved' | 'unknown';

export function stateName(state: OccurrenceState): StateName {
  switch (state) {
    case 1:
      return 'scheduled';
    case 2:
      return 'completed';
    case 3:
      return 'cancelled';
    case 4:
      return 'no_show';
    case 5:
      return 'moved';
    default:
      return 'unknown';
  }
}

export const STATE_LABELS: Record<StateName, string> = {
  scheduled: 'Scheduled',
  completed: 'Completed',
  cancelled: 'Cancelled',
  no_show: 'No-show',
  moved: 'Moved',
  unknown: 'Unknown',
};

export function cancellerName(by: Canceller): string {
  switch (by) {
    case 1:
      return 'the family';
    case 2:
      return 'the studio';
    case 3:
      return 'a closure';
    default:
      return '';
  }
}

export type KindName = 'single' | 'package' | 'recurring';

export const KINDS: Record<KindName, { label: string; value: OfferingKind }> = {
  single: { label: 'Single lesson', value: 1 as OfferingKind },
  package: { label: 'Package', value: 2 as OfferingKind },
  recurring: { label: 'Monthly', value: 3 as OfferingKind },
};

export function kindName(kind: OfferingKind): KindName {
  switch (kind) {
    case 2:
      return 'package';
    case 3:
      return 'recurring';
    default:
      return 'single';
  }
}

/** "$35.00" from cents. */
export function money(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`;
}

/** Cents from "35" or "35.50", or NaN. */
export function centsOf(value: string): number {
  const trimmed = value.trim().replace(/^\$/, '');
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return NaN;
  return Math.round(Number(trimmed) * 100);
}

/** What an audit command reads as in the history. */
export const COMMAND_LABELS: Record<string, string> = {
  cancel_occurrence: 'Cancelled',
  complete_occurrence: 'Marked completed',
  no_show_occurrence: 'Marked no-show',
  revert_occurrence: 'Reverted to scheduled',
  move_occurrence: 'Moved',
  schedule_make_up: 'Make-up scheduled',
  reassign_occurrence: 'Reassigned',
  end_series: 'Series ended',
  close_studio: 'Studio closed',
};
