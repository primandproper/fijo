<script lang="ts">
  import { untrack } from 'svelte';
  import { zoneName } from '$lib/zones';
  import { enhance } from '$app/forms';
  import { resolve } from '$app/paths';
  import SeriesFields from '$lib/components/SeriesFields.svelte';

  let { data, form } = $props();

  let query = $state('');

  // The new client's people, bound so the first lesson's "for" can name them as they are typed. A
  // form that came back refused (without JavaScript) starts from what was sent.
  const sent = untrack(() => form?.values);
  let contactName = $state(sent?.contactName ?? '');
  let attends = $state(sent?.attends ?? false);
  let children = $state(sent?.children ?? '');
  let withLesson = $state(sent?.withLesson ?? true);

  const lessonChoices = $derived.by(() => {
    const choices: { value: string; label: string }[] = [];
    if (attends && contactName.trim()) choices.push({ value: 'contact', label: contactName.trim() });
    children
      .split('\n')
      .map((n) => n.trim())
      .filter(Boolean)
      .forEach((child, i) => choices.push({ value: `child:${i}`, label: child }));
    if (choices.length > 1) choices.push({ value: 'all', label: 'All of them, taught together' });
    return choices;
  });

  const shown = $derived(
    data.households.filter((h) => {
      const q = query.trim().toLowerCase();
      const digits = q.replace(/\D/g, '');
      if (!q) return true;
      return (
        h.name.toLowerCase().includes(q) ||
        h.attendees.some((a) => a.name.toLowerCase().includes(q)) ||
        h.contacts.some((c) => c.name.toLowerCase().includes(q) || (digits !== '' && c.phone.includes(digits)))
      );
    }),
  );
</script>

<div class="wide">
  <h1>Clients</h1>
  <p class="muted">
    A client is a household: the people you text, and the students who take lessons. A parent and their children are one
    client; an adult student is a client on their own.
  </p>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  {#if data.households.length > 0}
    <input class="search" type="search" placeholder="Find a client, student or phone" bind:value={query} />
    <section class="card list">
      {#each shown as household (household.id)}
        <a class="item" href={resolve('/clients/[id]', { id: household.id })}>
          <div>
            <strong>{household.name}</strong>
            <div>
              {#if household.attendees.length > 0}
                {household.attendees.map((a) => a.name).join(', ')}
              {:else}
                <span class="muted">No students yet</span>
              {/if}
            </div>
            <div class="muted">
              {#each household.contacts as contact, i (contact.id)}{i > 0 ? ' · ' : ''}{contact.name}
                {contact.displayPhone}{/each}
            </div>
          </div>
          <div class="lessons muted">
            {#if household.lessons.length > 0}
              {household.lessons.join(', ')}
            {:else}
              no standing lessons
            {/if}
          </div>
        </a>
      {:else}
        <p class="item muted">Nobody matches “{query}”.</p>
      {/each}
    </section>
  {/if}

  <section class="card">
    <h2>Add a client</h2>
    <form method="POST" action="?/create" use:enhance>
      <div class="row two">
        <div>
          <label for="contactName">Who you text</label>
          <input id="contactName" name="contactName" bind:value={contactName} required />
        </div>
        <div>
          <label for="phone">Their phone</label>
          <input
            id="phone"
            name="phone"
            type="tel"
            inputmode="tel"
            value={form?.values?.phone ?? ''}
            placeholder="(512) 555-0100"
            required
          />
        </div>
      </div>
      <label class="check">
        <input type="checkbox" name="attends" bind:checked={attends} />
        They take lessons themselves
      </label>
      <label for="children">Children or others who take lessons <span class="muted">(one per line)</span></label>
      <textarea id="children" name="children" rows="3" bind:value={children}></textarea>
      <div class="row two">
        <div>
          <label for="name">Client name <span class="muted">(optional)</span></label>
          <input id="name" name="name" value={form?.values?.name ?? ''} placeholder="García family" />
        </div>
        <div>
          <label for="language">Texts in</label>
          <select id="language" name="language">
            <option value="en">English</option>
            <option value="es">Spanish</option>
          </select>
        </div>
      </div>

      {#if data.offerings.length > 0}
        <label class="check">
          <input type="checkbox" name="withLesson" bind:checked={withLesson} />
          Set up their standing lesson now
        </label>
        {#if withLesson}
          <fieldset class="first-lesson">
            <legend>Their standing lesson</legend>
            {#if lessonChoices.length > 1}
              <label for="lessonFor">For</label>
              <select id="lessonFor" name="lessonFor" required>
                {#each lessonChoices as choice (choice.value)}
                  <option value={choice.value}>{choice.label}</option>
                {/each}
              </select>
            {/if}
            <SeriesFields prefix="first" offerings={data.offerings} today={data.today} pickWho={false} />
            <p class="muted">
              Times are in the studio's zone, {zoneName(data.studioZone)}. More lessons, for this student or the others,
              can be added from the client's page.
            </p>
          </fieldset>
        {/if}
      {:else}
        <p class="muted">
          Add <a href={resolve('/offerings')}>what you teach</a> to set up a client's standing lesson as you add them.
        </p>
      {/if}

      <button type="submit">{withLesson && data.offerings.length > 0 ? 'Add client and lesson' : 'Add client'}</button>
    </form>
  </section>
</div>

<style>
  .search {
    margin-bottom: var(--space-md);
  }
  .list {
    padding: 0;
    margin-bottom: var(--space-md);
  }
  .item {
    display: flex;
    justify-content: space-between;
    gap: var(--space-md);
    padding: var(--space-md) var(--space-lg);
    color: inherit;
    text-decoration: none;
    margin: 0;
  }
  a.item:hover {
    background: var(--color-info-bg);
  }
  .item + .item {
    border-top: 1px solid var(--color-line);
  }
  .lessons {
    text-align: right;
    flex-shrink: 0;
    max-width: 40%;
  }
  .row.two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-md);
  }
  .check {
    display: flex;
    gap: var(--space-sm);
    align-items: center;
    font-weight: 400;
  }
  .check input {
    width: auto;
    margin: 0;
  }
  .first-lesson {
    border: 1px solid var(--color-line);
    border-radius: var(--radius);
    padding: var(--space-md);
    margin: 0 0 var(--space-md);
  }
  .first-lesson legend {
    font-weight: 600;
    padding: 0 var(--space-sm);
  }
  textarea {
    width: 100%;
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--color-line);
    border-radius: var(--radius);
    font: inherit;
    margin-bottom: var(--space-md);
    background: var(--color-surface);
    resize: vertical;
  }
  @media (max-width: 640px) {
    .row.two {
      grid-template-columns: 1fr;
      gap: 0;
    }
    .item {
      flex-direction: column;
    }
    .lessons {
      text-align: left;
      max-width: none;
    }
  }
</style>
