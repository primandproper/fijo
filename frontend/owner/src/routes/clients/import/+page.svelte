<script lang="ts">
  import { enhance } from '$app/forms';
  import { resolve } from '$app/paths';
  import { displayPhone } from '$lib/roster';
  import { money } from '$lib/schedule';
  import { displayClock, displayDate, WEEKDAYS } from '$lib/time';

  let { form } = $props();

  let previewing = $state(false);
  let applying = $state(false);

  const preview = $derived(form && 'preview' in form ? form.preview : null);
  const files = $derived(form && 'files' in form ? form.files : null);
  const result = $derived(form && 'result' in form ? form.result : null);

  type Planned = NonNullable<typeof preview>['households'][number];

  // A client already here brings only what they are missing: a contact whose email and phone are both
  // new, and the lessons and one-offs they have none of. The preview has already left the rest out.
  function hasNew(h: Planned): boolean {
    return h.lessons.length + h.appointments.length > 0 || h.contacts.some((c) => !c.onRoster);
  }

  const toAdd = $derived(preview?.households.filter((h) => h.active && !h.onRoster) ?? []);
  const updates = $derived(preview?.households.filter((h) => h.active && h.onRoster && hasNew(h)) ?? []);
  const inactive = $derived(preview?.households.filter((h) => !h.active && (!h.onRoster || hasNew(h))) ?? []);
  const unchanged = $derived(preview?.households.filter((h) => h.onRoster && !hasNew(h)) ?? []);
  const ticked = $derived([...toAdd, ...updates]);
  const lessonCount = $derived(ticked.reduce((n, h) => n + h.lessons.length, 0));
  const oneOffCount = $derived(ticked.reduce((n, h) => n + h.appointments.length, 0));
  // Only what a lesson being added is sold as: a type whose lessons are all already here is never
  // written, so listing it would promise an offering that does not arrive.
  const used = $derived(new Set(ticked.flatMap((h) => [...h.lessons, ...h.appointments].map((l) => l.offeringKey))));
  const offerings = $derived(preview?.offerings.filter((o) => used.has(o.key)) ?? []);
  const unmatched = $derived(preview?.columns.filter((c) => !c.header) ?? []);
  const matched = $derived(preview?.columns.filter((c) => c.header) ?? []);

  function every(weeks: number): string {
    return weeks === 2 ? 'every other' : 'every';
  }

  function priceOf(cents: number): string {
    return cents < 0 ? 'no price' : money(cents);
  }
</script>

<div class="wide">
  <p><a href={resolve('/clients')}>← Clients</a></p>
  <h1>Bring your clients over from Acuity</h1>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  {#if result}
    <section class="card">
      <h2>Done</h2>
      <ul>
        <li>{result.householdsWritten} clients added</li>
        {#if result.householdsUpdated > 0}
          <li>{result.householdsUpdated} clients already here brought up to date</li>
        {/if}
        <li>{result.lessonsWritten} standing lessons added, and on the week view</li>
        {#if result.appointmentsWritten > 0}
          <li>{result.appointmentsWritten} one-off lessons booked</li>
        {/if}
        {#if result.offeringsWritten > 0}
          <li>
            {result.offeringsWritten} new offerings, named after Acuity's appointment types. Give them Spanish names on
            <a href={resolve('/offerings')}>Offerings</a>.
          </li>
        {/if}
        {#if result.householdsOnRoster > 0}
          <li>{result.householdsOnRoster} were already your clients with nothing new, and were left as they are</li>
        {/if}
        {#if result.householdsSkipped > 0}
          <li>{result.householdsSkipped} you left out</li>
        {/if}
      </ul>
      <p>
        <a href={resolve('/clients')}>See your clients</a> · <a href={resolve('/week')}>See the week</a>
      </p>
      <p class="muted">
        You can run the import again whenever you like, say on the day you switch. Clients already here are recognized
        by their email or phone and are not added twice; only lessons Acuity has and Fijo does not are added.
      </p>
    </section>
  {:else if preview && files}
    <form
      method="POST"
      action="?/apply"
      use:enhance={() => {
        applying = true;
        return async ({ update }) => {
          await update();
          applying = false;
        };
      }}
    >
      <textarea name="clientsCsv" hidden>{files.clientsCsv}</textarea>
      <textarea name="appointmentsCsv" hidden>{files.appointmentsCsv}</textarea>
      <input type="hidden" name="language" value={files.language} />

      <section class="card">
        <h2>What will be added</h2>
        <p>
          {toAdd.length} new clients with lessons coming up{updates.length > 0
            ? `, ${updates.length} clients already here with something new`
            : ''}, {lessonCount} standing lessons and {oneOffCount} one-off lessons. Nothing is added until you press the
          button at the bottom. Untick anything that is wrong; you can fix the rest on each client's page afterwards.
        </p>
        <p class="muted">
          Reminders go to the email or phone shown. Everybody starts in {files.language === 'es'
            ? 'Spanish'
            : 'English'}.
        </p>
      </section>

      {#if preview.warnings.length > 0}
        <section class="card">
          <h2>About the files</h2>
          <ul class="muted">
            {#each preview.warnings as warning, i (i)}
              <li>{warning}</li>
            {/each}
          </ul>
        </section>
      {/if}

      {#each toAdd as household (household.key)}
        {@render householdCard(household, true)}
      {/each}

      {#if updates.length > 0}
        <h2>Already your clients, with something new</h2>
        <p class="muted">
          Recognized by their email or phone. Only what Fijo does not have yet is added; nothing already here is
          changed.
        </p>
        {#each updates as household (household.key)}
          {@render householdCard(household, true)}
        {/each}
      {/if}

      {#if inactive.length > 0}
        <details class="card">
          <summary>{inactive.length} clients with nothing coming up</summary>
          <p class="muted">Not added unless you tick them: Acuity's client list has everybody who ever booked.</p>
          {#each inactive as household (household.key)}
            {@render householdCard(household, false)}
          {/each}
        </details>
      {/if}

      {#if unchanged.length > 0}
        <details class="card">
          <summary>{unchanged.length} already your clients, with nothing new</summary>
          <p class="muted">
            Their email or phone is already on a client here, and Fijo already has their lessons, so they are left as
            they are.
          </p>
          <ul>
            {#each unchanged as household (household.key)}
              <li>{household.existingName || household.name}</li>
            {/each}
          </ul>
        </details>
      {/if}

      {#if offerings.length > 0}
        <section class="card">
          <h2>Offerings</h2>
          <ul>
            {#each offerings as offering (offering.key)}
              <li>
                <strong>{offering.name}</strong>
                {#if offering.existing}
                  <span class="muted">· already yours</span>
                {:else}
                  <span class="muted">
                    · new: {offering.durationMinutes} minutes, {priceOf(offering.listPrice)} a lesson
                  </span>
                {/if}
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      <details class="card">
        <summary>How the files were read</summary>
        <ul class="muted">
          {#each matched as column, i (i)}
            <li>{column.file}: “{column.header}” as {column.field}</li>
          {/each}
          {#each unmatched as column, i (i)}
            <li>{column.file}: no column for {column.field}</li>
          {/each}
        </ul>
      </details>

      <div class="actions">
        <button type="submit" disabled={applying}>{applying ? 'Adding…' : 'Add them'}</button>
        <a href={resolve('/clients/import')}>Start over</a>
      </div>
    </form>
  {:else}
    <section class="card">
      <p>
        Fijo reads Acuity's two exports. Your clients are not asked to do anything: they keep getting reminders, now
        from Fijo, at the email or phone Acuity has for them.
      </p>
      <ol>
        <li>
          In Acuity, go to <strong>Clients → Import/Export</strong> and choose <strong>Export client list</strong>.
        </li>
        <li>
          Go to <strong>Reports → Import/Export</strong> and export appointments from today to at least eight weeks out, so
          each standing lesson shows up more than once. Leave canceled appointments out.
        </li>
        <li>Choose both files below. Nothing is added until you have looked it over.</li>
      </ol>

      <form
        method="POST"
        action="?/preview"
        enctype="multipart/form-data"
        use:enhance={() => {
          previewing = true;
          return async ({ update }) => {
            await update({ reset: false });
            previewing = false;
          };
        }}
      >
        <div class="row two">
          <div>
            <label for="clients">Client list</label>
            <input id="clients" name="clients" type="file" accept=".csv,text/csv" />
          </div>
          <div>
            <label for="appointments">Appointments</label>
            <input id="appointments" name="appointments" type="file" accept=".csv,text/csv" />
          </div>
        </div>
        <label for="language">Reminders start in</label>
        <select id="language" name="language">
          <option value="en">English</option>
          <option value="es">Spanish</option>
        </select>
        <button type="submit" disabled={previewing}>{previewing ? 'Reading…' : 'Look it over'}</button>
      </form>
    </section>
  {/if}
</div>

{#snippet householdCard(household: Planned, checked: boolean)}
  {@const booked = new Set([...household.lessons, ...household.appointments].map((l) => l.who))}
  <section class="card household">
    <input type="hidden" name="householdShown" value={household.key} />
    <label class="check head">
      <input type="checkbox" name="household" value={household.key} {checked} />
      <strong>{household.onRoster ? household.existingName : household.name}</strong>
      {#if household.onRoster}<span class="muted">· already here</span>{/if}
    </label>
    <ul class="people">
      {#each household.contacts.filter((c) => !household.onRoster || !c.onRoster) as contact, i (i)}
        <li>
          {contact.name}
          <span class="muted">
            · {contact.self ? 'takes lessons, gets reminders' : 'gets reminders'}
            {#if contact.email}· {contact.email}{/if}
            {#if contact.phone}· {displayPhone(contact.phone)}{/if}
          </span>
          {#if household.onRoster}<span class="tag">new contact</span>{/if}
          {#if contact.guessedName}<span class="tag">name guessed</span>{/if}
        </li>
      {/each}
      <!-- On a client already here, only the people being added: a student with something to book. -->
      {#each household.attendees.filter((a) => !household.contacts.some((c) => c.self && c.name === a.name && !c.onRoster) && (!household.onRoster || (!a.onRoster && booked.has(a.name)))) as attendee (attendee.name)}
        <li>
          {attendee.name} <span class="muted">· takes lessons</span>
          {#if household.onRoster}<span class="tag">new student</span>{/if}
        </li>
      {/each}
    </ul>
    {#if household.notes && !household.onRoster}
      <p class="muted">{household.notes}</p>
    {/if}
    {#each household.lessons as lesson (lesson.key)}
      <input type="hidden" name="lessonShown" value={lesson.key} />
      <label class="check lesson">
        <input type="checkbox" name="lesson" value={lesson.key} checked />
        <span>
          {lesson.who}: {lesson.offering}, {every(lesson.intervalWeeks)}
          {WEEKDAYS[lesson.weekday]} at {displayClock(lesson.startMinutes)}, from {displayDate(lesson.startsOn)}
          <span class="muted">· {priceOf(lesson.price)} · seen {lesson.seen} times in Acuity</span>
          {#each lesson.warnings as warning, i (i)}
            <span class="warning">{warning}</span>
          {/each}
        </span>
      </label>
    {/each}
    {#each household.appointments as appointment (appointment.key)}
      <input type="hidden" name="appointmentShown" value={appointment.key} />
      <label class="check lesson">
        <input type="checkbox" name="appointment" value={appointment.key} checked />
        <span>
          {appointment.who}: {appointment.offering}, one lesson on {displayDate(appointment.on)} at {displayClock(
            appointment.startMinutes,
          )}
          {#if appointment.durationMinutes}<span class="muted">· {appointment.durationMinutes} minutes</span>{/if}
          <span class="muted reason">{appointment.reason}</span>
        </span>
      </label>
    {/each}
    {#each household.warnings as warning, i (i)}
      <p class="warning">{warning}</p>
    {/each}
  </section>
{/snippet}

<style>
  .household .head {
    font-size: 1.05rem;
  }
  .people {
    margin: 0.25rem 0 0.5rem 1.75rem;
    padding: 0;
    list-style: none;
  }
  .lesson {
    margin-left: 1.75rem;
    align-items: flex-start;
  }
  .reason {
    display: block;
    font-size: 0.9rem;
  }
  .warning {
    display: block;
    color: #8a5a00;
    font-size: 0.9rem;
  }
  .actions {
    display: flex;
    gap: 1rem;
    align-items: center;
    margin: 1rem 0 2rem;
  }
  details > summary {
    cursor: pointer;
    font-weight: 600;
  }
</style>
