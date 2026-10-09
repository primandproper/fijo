/**
 * Time zones as a person picks them: by the clock they live on, not by IANA name.
 *
 * The common zones for Fijo's studios and their clients are named by hand, grouped by where they
 * are. Everything else the runtime knows follows, named by its city. The value is always the IANA
 * name, which is what the API stores.
 */

interface CommonZone {
  zone: string;
  /** What a person calls it: "Central Time". */
  name: string;
  /** Where it is, so the right one is findable: "Chicago, Dallas, Houston". */
  places: string;
}

const COMMON: { region: string; zones: CommonZone[] }[] = [
  {
    region: 'United States & Canada',
    zones: [
      { zone: 'America/New_York', name: 'Eastern Time', places: 'New York, Miami, Toronto' },
      { zone: 'America/Chicago', name: 'Central Time', places: 'Chicago, Dallas, Houston, Austin' },
      { zone: 'America/Denver', name: 'Mountain Time', places: 'Denver, Salt Lake City, El Paso' },
      { zone: 'America/Phoenix', name: 'Arizona', places: 'Phoenix, no daylight saving' },
      { zone: 'America/Los_Angeles', name: 'Pacific Time', places: 'Los Angeles, Seattle, Vancouver' },
      { zone: 'America/Anchorage', name: 'Alaska', places: 'Anchorage' },
      { zone: 'Pacific/Honolulu', name: 'Hawaii', places: 'Honolulu' },
      { zone: 'America/Halifax', name: 'Atlantic Time', places: 'Halifax' },
      { zone: 'America/St_Johns', name: 'Newfoundland', places: "St. John's" },
      { zone: 'America/Puerto_Rico', name: 'Puerto Rico', places: 'San Juan' },
    ],
  },
  {
    region: 'México',
    zones: [
      { zone: 'America/Mexico_City', name: 'Central Mexico', places: 'Mexico City, Guadalajara, Monterrey' },
      { zone: 'America/Cancun', name: 'Quintana Roo', places: 'Cancún, Playa del Carmen' },
      { zone: 'America/Chihuahua', name: 'Chihuahua', places: 'Chihuahua' },
      { zone: 'America/Mazatlan', name: 'Pacific Mexico', places: 'Mazatlán, Culiacán, Tepic, La Paz' },
      { zone: 'America/Hermosillo', name: 'Sonora', places: 'Hermosillo' },
      { zone: 'America/Tijuana', name: 'Baja California', places: 'Tijuana, Mexicali, Ensenada' },
    ],
  },
  {
    region: 'Central America & the Caribbean',
    zones: [
      { zone: 'America/Guatemala', name: 'Guatemala', places: 'Guatemala City' },
      { zone: 'America/El_Salvador', name: 'El Salvador', places: 'San Salvador' },
      { zone: 'America/Tegucigalpa', name: 'Honduras', places: 'Tegucigalpa' },
      { zone: 'America/Managua', name: 'Nicaragua', places: 'Managua' },
      { zone: 'America/Costa_Rica', name: 'Costa Rica', places: 'San José' },
      { zone: 'America/Panama', name: 'Panamá', places: 'Panama City' },
      { zone: 'America/Santo_Domingo', name: 'Dominican Republic', places: 'Santo Domingo' },
      { zone: 'America/Havana', name: 'Cuba', places: 'Havana' },
    ],
  },
  {
    region: 'South America',
    zones: [
      { zone: 'America/Bogota', name: 'Colombia', places: 'Bogotá, Medellín' },
      { zone: 'America/Lima', name: 'Perú', places: 'Lima' },
      { zone: 'America/Caracas', name: 'Venezuela', places: 'Caracas' },
      { zone: 'America/Santiago', name: 'Chile', places: 'Santiago' },
      { zone: 'America/Argentina/Buenos_Aires', name: 'Argentina', places: 'Buenos Aires' },
      { zone: 'America/Sao_Paulo', name: 'Brasília Time', places: 'São Paulo, Rio de Janeiro' },
    ],
  },
  {
    region: 'Europe',
    zones: [
      { zone: 'Europe/London', name: 'United Kingdom', places: 'London' },
      { zone: 'Europe/Madrid', name: 'Spain', places: 'Madrid, Barcelona' },
    ],
  },
];

const BY_ZONE = new Map(COMMON.flatMap((g) => g.zones.map((z) => [z.zone, z])));

// Spellings the runtime and the API both accept for zones already listed above.
const ALIASES: Record<string, string> = {
  'US/Eastern': 'America/New_York',
  'US/Central': 'America/Chicago',
  'US/Mountain': 'America/Denver',
  'US/Arizona': 'America/Phoenix',
  'US/Pacific': 'America/Los_Angeles',
  'US/Alaska': 'America/Anchorage',
  'US/Hawaii': 'Pacific/Honolulu',
  'America/Buenos_Aires': 'America/Argentina/Buenos_Aires',
};

/** The city an IANA name ends in: "America/Argentina/Buenos_Aires" is "Buenos Aires". */
function cityOf(zone: string): string {
  return (zone.split('/').pop() ?? zone).replaceAll('_', ' ');
}

/** "Central Time" for a zone a person knows by name, the city for any other. */
export function zoneName(zone: string): string {
  if (!zone) return '';
  const common = BY_ZONE.get(ALIASES[zone] ?? zone);
  if (common) return common.name;
  return zone === 'UTC' || zone === 'Etc/UTC' ? 'UTC' : cityOf(zone);
}

/** "GMT-5" at an instant, which is how the zone's clock reads against everybody else's right now. */
function offsetOf(zone: string, at: Date): string {
  try {
    const part = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'shortOffset' })
      .formatToParts(at)
      .find((p) => p.type === 'timeZoneName');
    return part?.value ?? '';
  } catch {
    return '';
  }
}

export interface ZoneGroup {
  label: string;
  options: { value: string; label: string }[];
}

/**
 * Every zone a picker offers, grouped: the common ones by region, then the rest. A current value
 * the lists do not hold under that spelling is kept, so saving the form never changes it unasked.
 */
export function zoneGroups(at: Date, current = ''): ZoneGroup[] {
  const groups: ZoneGroup[] = COMMON.map((g) => ({
    label: g.region,
    options: g.zones.map((z) => ({ value: z.zone, label: `${z.name} (${offsetOf(z.zone, at)}) · ${z.places}` })),
  }));

  const listed = new Set(BY_ZONE.keys());
  let everywhere: string[] = [];
  try {
    everywhere = Intl.supportedValuesOf('timeZone');
  } catch {
    everywhere = [];
  }
  const rest = everywhere
    .filter((zone) => !listed.has(ALIASES[zone] ?? zone) && zone.includes('/'))
    .map((zone) => ({ value: zone, label: `${cityOf(zone)} (${offsetOf(zone, at)}) · ${zone.split('/')[0]}` }))
    .sort((a, b) => a.label.localeCompare(b.label));
  groups.push({ label: 'Everywhere else', options: [...rest, { value: 'UTC', label: 'UTC (GMT)' }] });

  if (current && !groups.some((g) => g.options.some((o) => o.value === current))) {
    groups.unshift({ label: 'Current', options: [{ value: current, label: `${zoneName(current)} (${current})` }] });
  }

  return groups;
}
