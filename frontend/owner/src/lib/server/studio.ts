import { api } from '$lib/grpc/clients';
import { isZone } from '$lib/time';

export interface StudioContext {
  userId: string;
  studioName: string;
  /** The zone the studio's rules are written in: a series at four is four here. */
  studioZone: string;
  /** The zone the grid is drawn in: the owner's own if they set one, else the studio's. */
  zone: string;
}

function usable(zone: string | undefined): string | undefined {
  return zone && isZone(zone) ? zone : undefined;
}

/** Who is asking and which zones their grid lives in. */
export async function studioContext(token: string): Promise<StudioContext> {
  const self = await api.owners.getSelf(token);
  const studioZone = usable(self.studio?.timeZone) ?? 'UTC';
  return {
    userId: self.user?.id ?? '',
    studioName: self.studio?.name ?? '',
    studioZone,
    zone: usable(self.timeZone) ?? studioZone,
  };
}
