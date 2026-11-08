<script lang="ts">
  import { enhance } from '$app/forms';
  import ReminderList from '$lib/components/ReminderList.svelte';

  let { data, form } = $props();
</script>

<div class="wide">
  <h1>Reminders</h1>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  <section class="card">
    <h2>{data.remindersOn ? 'Reminders are on' : 'Reminders are off'}</h2>
    <p class="muted">
      Before each lesson, every client with an email gets a reminder in their own language, from
      {data.studioName || 'your studio'}. By default that is a day before and an hour before; you can change it for each
      client on their page.
    </p>
    {#if data.remindersOn}
      <p class="muted">On since {data.onSince}.</p>
    {:else}
      <p class="muted">
        Turn them on when you turn off reminders in Acuity, so nobody gets two. Reminders already due when you turn them
        on are not sent.
      </p>
    {/if}
    {#if form?.saved}<div class="alert info">Saved. Reminders are {form.saved}.</div>{/if}
    <form
      method="POST"
      action="?/save"
      use:enhance={() =>
        ({ update }) =>
          update({ reset: false })}
    >
      <label for="replyTo">Where your clients write back to</label>
      <input id="replyTo" name="replyTo" type="email" value={data.replyTo} placeholder="you@example.com" />
      <p class="muted hint">
        Every reminder says to write here to change or cancel{data.replyToSaved ? '' : ' (this is your own address)'}.
      </p>
      <label class="switch">
        <input type="checkbox" name="remindersOn" checked={data.remindersOn} />
        Send reminders
      </label>
      <button type="submit">Save</button>
    </form>
  </section>

  <section class="card">
    <h2>See one first</h2>
    <p class="muted">A sample reminder, about a made-up lesson tomorrow, sent to the address above.</p>
    {#if form?.testedTo}<div class="alert info">Sent to {form.testedTo}.</div>{/if}
    <form method="POST" action="?/test" use:enhance class="inline">
      <select name="language" aria-label="Language">
        <option value="en">In English</option>
        <option value="es">In Spanish</option>
      </select>
      <button type="submit" class="secondary" disabled={!data.replyToSaved}>Send me a test</button>
    </form>
    {#if !data.replyToSaved}<p class="muted hint">Save an address above first.</p>{/if}
  </section>

  <section class="card">
    <h2>Recently</h2>
    <ReminderList reminders={data.reminders} showHousehold />
  </section>
</div>

<style>
  .switch {
    display: flex;
    gap: var(--space-sm);
    align-items: center;
    margin: var(--space-md) 0;
    font-weight: 600;
  }
  .switch input {
    width: auto;
  }
  .inline {
    display: flex;
    gap: var(--space-sm);
    align-items: center;
    flex-wrap: wrap;
  }
  .inline select {
    width: auto;
  }
  .hint {
    margin-top: 0.25rem;
    font-size: 0.9rem;
  }
</style>
