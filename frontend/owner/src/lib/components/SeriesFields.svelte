<script lang="ts">
  import { clockOf, WEEKDAYS } from '$lib/time';

  interface Values {
    id: string;
    offeringId: string;
    weekday: number;
    startMinute: number;
    intervalWeeks: number;
    startsOn: string;
    endsOn: string;
    priceOverride: number | null;
    staffUserIds: string[];
  }

  let {
    values = null,
    prefix,
    offerings,
    today,
  }: {
    values?: Values | null;
    prefix: string;
    offerings: { id: string; name: string }[];
    today: string;
  } = $props();
</script>

{#if values}
  <input type="hidden" name="id" value={values.id} />
  <input type="hidden" name="staff" value={values.staffUserIds.join(',')} />
{/if}

<label for="{prefix}-offering">Lesson</label>
<select id="{prefix}-offering" name="offeringId" value={values?.offeringId ?? offerings[0]?.id} required>
  {#each offerings as offering (offering.id)}
    <option value={offering.id}>{offering.name}</option>
  {/each}
</select>

<div class="row three">
  <div>
    <label for="{prefix}-weekday">Day</label>
    <select id="{prefix}-weekday" name="weekday" value={String(values?.weekday ?? 1)}>
      {#each WEEKDAYS as day, i (day)}
        <option value={String(i)}>{day}</option>
      {/each}
    </select>
  </div>
  <div>
    <label for="{prefix}-time">Time</label>
    <input id="{prefix}-time" name="time" type="time" value={values ? clockOf(values.startMinute) : '16:00'} required />
  </div>
  <div>
    <label for="{prefix}-interval">Every</label>
    <select id="{prefix}-interval" name="intervalWeeks" value={String(values?.intervalWeeks ?? 1)}>
      <option value="1">week</option>
      <option value="2">other week</option>
    </select>
  </div>
</div>

<div class="row three">
  <div>
    <label for="{prefix}-startsOn">Starting</label>
    <input id="{prefix}-startsOn" name="startsOn" type="date" value={values?.startsOn ?? today} required />
  </div>
  <div>
    <label for="{prefix}-endsOn">Until (optional)</label>
    <input id="{prefix}-endsOn" name="endsOn" type="date" value={values?.endsOn ?? ''} />
  </div>
  <div>
    <label for="{prefix}-price">Price override (optional)</label>
    <input
      id="{prefix}-price"
      name="priceOverride"
      inputmode="decimal"
      value={values?.priceOverride != null ? (values.priceOverride / 100).toFixed(2) : ''}
      placeholder="list price"
    />
  </div>
</div>

<style>
  .row.three {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: var(--space-md);
  }
  @media (max-width: 640px) {
    .row.three {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
</style>
