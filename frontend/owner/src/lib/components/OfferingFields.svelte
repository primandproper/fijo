<script lang="ts">
  import { KINDS, type KindName } from '$lib/schedule';

  interface Values {
    id: string;
    nameEn: string;
    nameEs: string;
    kind: KindName;
    durationMinutes: number;
    listPrice: number;
    lessonCount: number;
    expiresAfterDays: number;
    shareable: boolean;
  }

  let { values = null, prefix }: { values?: Values | null; prefix: string } = $props();

  // svelte-ignore state_referenced_locally
  let kind = $state<KindName>(values?.kind ?? 'single');
</script>

{#if values}<input type="hidden" name="id" value={values.id} />{/if}

<div class="row">
  <div>
    <label for="{prefix}-nameEn">Name</label>
    <input id="{prefix}-nameEn" name="nameEn" value={values?.nameEn ?? ''} placeholder="Half-hour piano" required />
  </div>
  <div>
    <label for="{prefix}-nameEs">Name in Spanish</label>
    <input id="{prefix}-nameEs" name="nameEs" value={values?.nameEs ?? ''} placeholder="Piano, media hora" />
  </div>
</div>
<div class="row three">
  <div>
    <label for="{prefix}-kind">Sold as</label>
    <select id="{prefix}-kind" name="kind" bind:value={kind}>
      {#each Object.entries(KINDS) as [value, k] (value)}
        <option {value}>{k.label}</option>
      {/each}
    </select>
  </div>
  <div>
    <label for="{prefix}-duration">Length (minutes)</label>
    <input
      id="{prefix}-duration"
      name="durationMinutes"
      type="number"
      min="5"
      step="5"
      value={values?.durationMinutes ?? 30}
      required
    />
  </div>
  <div>
    <label for="{prefix}-price">
      Price {kind === 'package' ? 'per package' : kind === 'recurring' ? 'per month' : 'per lesson'}
    </label>
    <input
      id="{prefix}-price"
      name="listPrice"
      inputmode="decimal"
      value={values ? (values.listPrice / 100).toFixed(2) : ''}
      placeholder="35.00"
      required
    />
  </div>
</div>
{#if kind === 'package'}
  <div class="row three">
    <div>
      <label for="{prefix}-count">Lessons in the package</label>
      <input id="{prefix}-count" name="lessonCount" type="number" min="1" value={values?.lessonCount || 4} />
    </div>
    <div>
      <label for="{prefix}-expires">Expires after (days, 0 = never)</label>
      <input
        id="{prefix}-expires"
        name="expiresAfterDays"
        type="number"
        min="0"
        value={values?.expiresAfterDays ?? 0}
      />
    </div>
    <div>
      <label class="inline" for="{prefix}-shareable">
        <input id="{prefix}-shareable" name="shareable" type="checkbox" checked={values?.shareable ?? false} />
        Siblings can share it
      </label>
    </div>
  </div>
{/if}

<style>
  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-md);
  }
  .row.three {
    grid-template-columns: 1fr 1fr 1fr;
  }
  @media (max-width: 640px) {
    .row,
    .row.three {
      grid-template-columns: 1fr;
      gap: 0;
    }
  }
  label.inline {
    display: flex;
    align-items: center;
    gap: var(--space-sm);
    margin-top: 2rem;
  }
  label.inline input {
    width: auto;
    margin: 0;
  }
</style>
