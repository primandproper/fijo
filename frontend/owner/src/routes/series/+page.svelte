<script lang="ts">
  import { enhance } from '$app/forms';
  import { resolve } from '$app/paths';
  import SeriesFields from '$lib/components/SeriesFields.svelte';
  import { money } from '$lib/schedule';
  import { displayClock, displayDate, WEEKDAYS } from '$lib/time';

  let { data, form } = $props();

  let editing = $state('');
  let ending = $state('');

  function who(ids: string[]): string {
    return ids.map((id) => data.studentNames[id] ?? 'Somebody no longer on the roster').join(' & ') || 'Nobody';
  }

  function closeOnSuccess() {
    return async ({ result, update }: { result: { type: string }; update: () => Promise<void> }) => {
      if (result.type === 'success') {
        editing = '';
        ending = '';
      }
      await update();
    };
  }
</script>

<div class="wide">
  <h1>Standing lessons</h1>
  <p class="muted">
    A standing lesson is somebody's lesson that repeats: Sofía, Tuesdays at four, every week. Times are in the studio's
    zone, {data.studioZone}. Changing one changes every lesson from now on that nobody has touched yet.
  </p>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  {#if data.offerings.length === 0}
    <div class="alert info">Add <a href={resolve('/offerings')}>something you teach</a> first.</div>
  {:else if data.students.length === 0}
    <div class="alert info">
      Add <a href={resolve('/clients')}>a client</a> first: every standing lesson is somebody's.
    </div>
  {:else}
    {#if data.series.length > 0}
      <section class="card list">
        {#each data.series as series (series.id)}
          <div class="item" class:ended={series.ended}>
            <div class="summary">
              <div>
                <strong>{who(series.attendeeIds)}</strong>
                <div>
                  {WEEKDAYS[series.weekday]}s at {displayClock(series.startMinute)}{#if series.intervalWeeks === 2},
                    every other week{/if} · {data.offeringNames[series.offeringId] ?? 'Unknown lesson'}
                </div>
                <div class="muted">
                  from {displayDate(series.startsOn)}
                  {#if series.endsOn}until {displayDate(series.endsOn)}{/if}
                  {#if series.priceOverride != null}· {money(series.priceOverride)}{/if}
                  {#if series.ended}· ended{/if}
                </div>
              </div>
              {#if !series.ended}
                <div class="buttons">
                  <button
                    type="button"
                    class="secondary"
                    onclick={() => {
                      editing = editing === series.id ? '' : series.id;
                      ending = '';
                    }}
                  >
                    {editing === series.id ? 'Close' : 'Edit'}
                  </button>
                  <button
                    type="button"
                    class="secondary"
                    onclick={() => {
                      ending = ending === series.id ? '' : series.id;
                      editing = '';
                    }}
                  >
                    End…
                  </button>
                </div>
              {/if}
            </div>

            {#if editing === series.id}
              <form method="POST" action="?/update" use:enhance={closeOnSuccess}>
                <SeriesFields
                  values={series}
                  prefix={series.id}
                  offerings={data.offerings}
                  today={data.today}
                  students={data.students}
                />
                <button type="submit">Save</button>
              </form>
            {/if}

            {#if ending === series.id}
              <form method="POST" action="?/end" use:enhance={closeOnSuccess}>
                <input type="hidden" name="id" value={series.id} />
                <p class="muted">Lessons after the last date are cancelled by the studio.</p>
                <label for="{series.id}-last">Last date</label>
                <input id="{series.id}-last" name="endsOn" type="date" value={data.today} required />
                <label for="{series.id}-why">Reason (optional)</label>
                <input id="{series.id}-why" name="reason" />
                <button type="submit" class="danger">End this standing lesson</button>
              </form>
            {/if}
          </div>
        {/each}
      </section>
    {/if}

    <section class="card">
      <h2>Add a standing lesson</h2>
      <!-- Kept filled after an add: lessons usually come in runs, and a reset would clear the date. -->
      <form
        method="POST"
        action="?/create"
        use:enhance={() =>
          ({ update }) =>
            update({ reset: false })}
      >
        <SeriesFields prefix="new" offerings={data.offerings} today={data.today} students={data.students} />
        <button type="submit">Add</button>
      </form>
    </section>
  {/if}
</div>

<style>
  .list {
    padding: 0;
    margin-bottom: var(--space-md);
  }
  .item {
    padding: var(--space-md) var(--space-lg);
  }
  .item + .item {
    border-top: 1px solid var(--color-line);
  }
  .item.ended {
    opacity: 0.6;
  }
  .summary {
    display: flex;
    justify-content: space-between;
    gap: var(--space-md);
    align-items: center;
  }
  .item form {
    margin-top: var(--space-md);
  }
  .buttons {
    display: flex;
    gap: var(--space-sm);
  }
</style>
