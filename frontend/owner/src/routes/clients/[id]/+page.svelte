<script lang="ts">
  import { enhance } from '$app/forms';
  import { resolve } from '$app/paths';
  import SeriesFields from '$lib/components/SeriesFields.svelte';
  import { LANGUAGES, RELATIONSHIPS } from '$lib/roster';
  import { money } from '$lib/schedule';
  import { displayClock, displayDate, WEEKDAYS } from '$lib/time';

  let { data, form } = $props();

  const household = $derived(data.household);

  // One thing open at a time: 'household', 'contact:<id>', 'student:<id>', 'lesson:<id>', or a 'new-' form.
  let open = $state('');

  function toggle(what: string) {
    open = open === what ? '' : what;
  }

  function closeOnSuccess() {
    return async ({ result, update }: { result: { type: string }; update: () => Promise<void> }) => {
      if (result.type === 'success') open = '';
      await update();
    };
  }

  function lessonsOf(studentId: string) {
    return data.lessons.filter((l) => l.attendeeIds.includes(studentId));
  }

  function others(ids: string[], studentId: string): string {
    const rest = ids.filter((id) => id !== studentId).map((id) => data.studentNames[id] ?? 'somebody');
    return rest.length > 0 ? `with ${rest.join(' & ')}` : '';
  }

  function contactName(id: string): string {
    return household.contacts.find((c) => c.id === id)?.name ?? '';
  }
</script>

<div class="wide">
  <p class="crumb"><a href={resolve('/clients')}>‹ Clients</a></p>

  <header class="head">
    <div>
      <h1>{household.name}</h1>
      <p class="muted">
        Texts in {LANGUAGES[household.language].label}{#if household.notes}
          · {household.notes}{/if}
      </p>
    </div>
    <button type="button" class="secondary" onclick={() => toggle('household')}>
      {open === 'household' ? 'Close' : 'Edit'}
    </button>
  </header>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  {#if open === 'household'}
    <section class="card">
      <form method="POST" action="?/updateHousehold" use:enhance={closeOnSuccess}>
        <label for="h-name">Client name</label>
        <input id="h-name" name="name" value={household.name} required />
        <label for="h-language">Texts in</label>
        <select id="h-language" name="language" value={household.language}>
          {#each Object.entries(LANGUAGES) as [value, l] (value)}
            <option {value}>{l.label}</option>
          {/each}
        </select>
        <label for="h-notes">Notes</label>
        <input id="h-notes" name="notes" value={household.notes} />
        <button type="submit">Save</button>
      </form>
    </section>
  {/if}

  <section class="card">
    <div class="section-head">
      <h2>Students</h2>
      <button type="button" class="secondary" onclick={() => toggle('new-student')}>
        {open === 'new-student' ? 'Close' : 'Add a student'}
      </button>
    </div>

    {#if open === 'new-student'}
      <form method="POST" action="?/saveStudent" use:enhance={closeOnSuccess} class="inline">
        <div class="row two">
          <div>
            <label for="new-student-name">Name</label>
            <input id="new-student-name" name="name" required />
          </div>
          <div>
            <label for="new-student-language">Language</label>
            <select id="new-student-language" name="language" value={household.language}>
              {#each Object.entries(LANGUAGES) as [value, l] (value)}
                <option {value}>{l.label}</option>
              {/each}
            </select>
          </div>
        </div>
        <button type="submit">Add</button>
      </form>
    {/if}

    {#each household.attendees as student (student.id)}
      <div class="person">
        <div class="summary">
          <div>
            <strong>{student.name}</strong>
            <span class="muted">
              · {LANGUAGES[student.language].label}{#if student.contactId}
                · texts as {contactName(student.contactId)}{/if}
            </span>
          </div>
          <div class="buttons">
            <button type="button" class="secondary" onclick={() => toggle(`lesson:${student.id}`)}>
              {open === `lesson:${student.id}` ? 'Close' : 'Add a standing lesson'}
            </button>
            <button type="button" class="secondary" onclick={() => toggle(`student:${student.id}`)}>
              {open === `student:${student.id}` ? 'Close' : 'Edit'}
            </button>
          </div>
        </div>

        {#each lessonsOf(student.id) as lesson (lesson.id)}
          <div class="lesson">
            {WEEKDAYS[lesson.weekday]}s at {displayClock(lesson.startMinute)}{#if lesson.intervalWeeks === 2}, every
              other week{/if}
            · {data.offeringNames[lesson.offeringId] ?? 'Unknown lesson'}
            <span class="muted">
              {others(lesson.attendeeIds, student.id)}
              · from {displayDate(lesson.startsOn)}{#if lesson.endsOn}
                until {displayDate(lesson.endsOn)}{/if}{#if lesson.priceOverride != null}
                · {money(lesson.priceOverride)}{/if}
            </span>
          </div>
        {:else}
          <p class="lesson muted">No standing lessons.</p>
        {/each}

        {#if open === `lesson:${student.id}`}
          {#if data.offerings.length === 0}
            <div class="alert info">Add <a href={resolve('/offerings')}>something you teach</a> first.</div>
          {:else}
            <form method="POST" action="?/addLesson" use:enhance={closeOnSuccess} class="inline">
              <SeriesFields
                prefix={`lesson-${student.id}`}
                offerings={data.offerings}
                today={data.today}
                student={student.id}
              />
              <p class="muted">Times are in the studio's zone, {data.studioZone}.</p>
              <button type="submit">Add standing lesson</button>
            </form>
          {/if}
        {/if}

        {#if open === `student:${student.id}`}
          <form method="POST" action="?/saveStudent" use:enhance={closeOnSuccess} class="inline">
            <input type="hidden" name="id" value={student.id} />
            <div class="row two">
              <div>
                <label for="{student.id}-name">Name</label>
                <input id="{student.id}-name" name="name" value={student.name} required />
              </div>
              <div>
                <label for="{student.id}-language">Language</label>
                <select id="{student.id}-language" name="language" value={student.language}>
                  {#each Object.entries(LANGUAGES) as [value, l] (value)}
                    <option {value}>{l.label}</option>
                  {/each}
                </select>
              </div>
            </div>
            <label for="{student.id}-contact">Texts as</label>
            <select id="{student.id}-contact" name="contactId" value={student.contactId}>
              <option value="">Nobody: a child, or texted through a guardian</option>
              {#each household.contacts as contact (contact.id)}
                <option value={contact.id}>{contact.name}</option>
              {/each}
            </select>
            <div class="buttons">
              <button type="submit">Save</button>
            </div>
          </form>
          <form method="POST" action="?/removeStudent" use:enhance={closeOnSuccess} class="inline">
            <input type="hidden" name="id" value={student.id} />
            <p class="muted">A student with standing lessons cannot be removed until those lessons are ended.</p>
            <button type="submit" class="danger">Remove {student.name}</button>
          </form>
        {/if}
      </div>
    {:else}
      <p class="muted">No students yet.</p>
    {/each}

    {#if data.lessons.length > 0}
      <p class="muted footnote">
        Change or end a standing lesson on <a href={resolve('/series')}>Lessons</a>.
      </p>
    {/if}
  </section>

  <section class="card">
    <div class="section-head">
      <h2>Contacts</h2>
      <button type="button" class="secondary" onclick={() => toggle('new-contact')}>
        {open === 'new-contact' ? 'Close' : 'Add a contact'}
      </button>
    </div>
    <p class="muted">The people who get texts about lessons, and who pay.</p>

    {#if open === 'new-contact'}
      <form method="POST" action="?/saveContact" use:enhance={closeOnSuccess} class="inline">
        {@render contactFields('new-contact', null)}
        <label class="check">
          <input type="checkbox" name="attends" />
          They take lessons themselves
        </label>
        <button type="submit">Add</button>
      </form>
    {/if}

    {#each household.contacts as contact (contact.id)}
      <div class="person">
        <div class="summary">
          <div>
            <strong>{contact.name}</strong>
            <span class="muted">· {RELATIONSHIPS[contact.relationship].label}</span>
            <div>
              <a href="tel:{contact.phone}">{contact.displayPhone}</a>
              <span class="muted">· {LANGUAGES[contact.language].label}</span>
              {#if contact.optedOut}<span class="tag">opted out of texts</span>{/if}
            </div>
          </div>
          <button type="button" class="secondary" onclick={() => toggle(`contact:${contact.id}`)}>
            {open === `contact:${contact.id}` ? 'Close' : 'Edit'}
          </button>
        </div>
        {#if open === `contact:${contact.id}`}
          <form method="POST" action="?/saveContact" use:enhance={closeOnSuccess} class="inline">
            <input type="hidden" name="id" value={contact.id} />
            {@render contactFields(contact.id, contact)}
            <button type="submit">Save</button>
          </form>
          <form method="POST" action="?/removeContact" use:enhance={closeOnSuccess} class="inline">
            <input type="hidden" name="id" value={contact.id} />
            <button type="submit" class="danger">Remove {contact.name}</button>
          </form>
        {/if}
      </div>
    {:else}
      <p class="muted">Nobody to text yet.</p>
    {/each}
  </section>

  <section class="card danger-zone">
    <h2>Archive this client</h2>
    <p class="muted">
      Archiving takes {household.name} and everybody on it off your roster. A client with standing lessons cannot be archived
      until those lessons are ended.
    </p>
    <form method="POST" action="?/archiveHousehold" use:enhance>
      <button type="submit" class="danger">Archive {household.name}</button>
    </form>
  </section>
</div>

{#snippet contactFields(
  prefix: string,
  contact: { name: string; phone: string; language: string; relationship: string } | null,
)}
  <div class="row two">
    <div>
      <label for="{prefix}-name">Name</label>
      <input id="{prefix}-name" name="name" value={contact?.name ?? ''} required />
    </div>
    <div>
      <label for="{prefix}-phone">Phone</label>
      <input
        id="{prefix}-phone"
        name="phone"
        type="tel"
        inputmode="tel"
        value={contact?.phone ?? ''}
        placeholder="(512) 555-0100"
        required
      />
    </div>
  </div>
  <div class="row two">
    <div>
      <label for="{prefix}-language">Texts in</label>
      <select id="{prefix}-language" name="language" value={contact?.language ?? household.language}>
        {#each Object.entries(LANGUAGES) as [value, l] (value)}
          <option {value}>{l.label}</option>
        {/each}
      </select>
    </div>
    {#if contact}
      <div>
        <label for="{prefix}-relationship">Is</label>
        <select id="{prefix}-relationship" name="relationship" value={contact.relationship}>
          {#each Object.entries(RELATIONSHIPS) as [value, r] (value)}
            <option {value}>{r.label}</option>
          {/each}
        </select>
      </div>
    {/if}
  </div>
{/snippet}

<style>
  .crumb {
    margin: 0 0 var(--space-sm);
  }
  .crumb a {
    text-decoration: none;
  }
  .head,
  .section-head,
  .summary {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-md);
  }
  .head {
    margin-bottom: var(--space-md);
  }
  .head h1 {
    margin: 0;
  }
  .head p {
    margin: 0;
  }
  .section-head h2 {
    margin: 0;
  }
  .section-head {
    margin-bottom: var(--space-sm);
  }
  .person {
    padding: var(--space-md) 0;
  }
  .person + .person {
    border-top: 1px solid var(--color-line);
  }
  .lesson {
    margin: var(--space-sm) 0 0 var(--space-md);
  }
  .buttons {
    display: flex;
    gap: var(--space-sm);
    flex-wrap: wrap;
    justify-content: flex-end;
  }
  form.inline {
    margin-top: var(--space-md);
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
  .tag {
    font-size: 0.85em;
    color: var(--color-danger);
    margin-left: var(--space-sm);
  }
  .footnote {
    margin: var(--space-md) 0 0;
  }
  .danger-zone h2 {
    font-size: 1.1em;
  }
  @media (max-width: 640px) {
    .row.two {
      grid-template-columns: 1fr;
      gap: 0;
    }
    .summary {
      flex-direction: column;
      align-items: flex-start;
    }
    .buttons {
      justify-content: flex-start;
    }
  }
</style>
