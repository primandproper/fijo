<script lang="ts">
  import { enhance } from '$app/forms';
  import OfferingFields from '$lib/components/OfferingFields.svelte';
  import { KINDS, money } from '$lib/schedule';

  let { data, form } = $props();

  let editing = $state('');
</script>

<div class="wide">
  <h1>What you teach</h1>
  <p class="muted">An offering is a kind of lesson and how it is sold. Standing slots on the grid each teach one.</p>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  {#if data.offerings.length > 0}
    <section class="card list">
      {#each data.offerings as offering (offering.id)}
        <div class="item">
          <div class="summary">
            <div>
              <strong>{offering.nameEn}</strong>
              {#if offering.nameEs && offering.nameEs !== offering.nameEn}
                <span class="muted">· {offering.nameEs}</span>
              {/if}
              <div class="muted">
                {KINDS[offering.kind].label} · {offering.durationMinutes} min · {money(offering.listPrice)}
                {#if offering.kind === 'package'}for {offering.lessonCount} lessons{/if}
                {#if offering.kind === 'recurring'}a month{/if}
              </div>
            </div>
            <div class="buttons">
              <button
                type="button"
                class="secondary"
                onclick={() => (editing = editing === offering.id ? '' : offering.id)}
              >
                {editing === offering.id ? 'Close' : 'Edit'}
              </button>
              <form method="POST" action="?/archive" use:enhance>
                <input type="hidden" name="id" value={offering.id} />
                <button type="submit" class="secondary">Retire</button>
              </form>
            </div>
          </div>
          {#if editing === offering.id}
            <form
              method="POST"
              action="?/update"
              use:enhance={() =>
                async ({ result, update }) => {
                  if (result.type === 'success') editing = '';
                  await update();
                }}
            >
              <OfferingFields values={offering} prefix={offering.id} />
              <button type="submit">Save</button>
            </form>
          {/if}
        </div>
      {/each}
    </section>
  {/if}

  <section class="card">
    <h2>Add an offering</h2>
    <form method="POST" action="?/create" use:enhance>
      <OfferingFields prefix="new" />
      <button type="submit">Add</button>
    </form>
  </section>
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
  .buttons form {
    margin: 0;
  }
</style>
