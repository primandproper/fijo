<script lang="ts">
  import { invalidateAll } from '$app/navigation';
  import { browser } from '$app/environment';
  import { enrolPasskey } from '$lib/passkeys';
  import { zoneName } from '$lib/zones';

  let { data, form } = $props();

  const supportsPasskeys = browser && typeof PublicKeyCredential !== 'undefined';
  let passkeyError = $state('');
  let passkeyName = $state('');

  async function addPasskey() {
    passkeyError = '';
    try {
      await enrolPasskey(passkeyName.trim());
      passkeyName = '';
      await invalidateAll();
    } catch (err) {
      passkeyError = err instanceof Error ? err.message : 'Passkey enrolment failed';
    }
  }
</script>

<div class="narrow">
  <h1>Hello, {data.user?.displayName || data.user?.username}</h1>

  {#if data.studio}
    <section class="card">
      <h2>{data.studio.name}</h2>
      <p class="muted">
        Studio time zone: {data.studio.timeZone ? zoneName(data.studio.timeZone) : 'not set'}
      </p>
      <p class="muted">Your roles: {data.roles.join(', ') || 'none'}</p>
    </section>
  {:else}
    <section class="card">
      <p>You do not belong to a studio yet.</p>
    </section>
  {/if}

  <section class="card">
    <h2>Your time zone</h2>
    <p class="muted">
      The week is drawn in your time zone. Most people want the studio's; pick another if you teach or travel somewhere
      else.
    </p>
    {#if form?.error}
      <div class="alert error">{form.error}</div>
    {/if}
    {#if form?.saved}
      <div class="alert info">Saved.</div>
    {/if}
    <form method="POST" action="?/setTimeZone">
      <label for="timeZone">Time zone</label>
      <select id="timeZone" name="timeZone" value={data.timeZone}>
        <option value="">
          Same as the studio{data.studio?.timeZone ? ` (${zoneName(data.studio.timeZone)})` : ''}
        </option>
        {#each data.zoneGroups as group (group.label)}
          <optgroup label={group.label}>
            {#each group.options as option (option.value)}
              <option value={option.value}>{option.label}</option>
            {/each}
          </optgroup>
        {/each}
      </select>
      <button type="submit">Save</button>
    </form>
  </section>

  <section class="card">
    <h2>Passkeys</h2>
    {#if data.passkeys.length === 0}
      <p class="muted">No passkeys yet.</p>
    {:else}
      <ul>
        {#each data.passkeys as passkey (passkey.id)}
          <li>
            {passkey.friendlyName}
            <span class="muted">added {passkey.createdAt.slice(0, 10)}</span>
            <form method="POST" action="?/archivePasskey" style="display: inline">
              <input type="hidden" name="id" value={passkey.id} />
              <button class="secondary" type="submit">Remove</button>
            </form>
          </li>
        {/each}
      </ul>
    {/if}
    {#if supportsPasskeys}
      {#if passkeyError}
        <div class="alert error">{passkeyError}</div>
      {/if}
      <label for="passkeyName">Name this device</label>
      <input id="passkeyName" bind:value={passkeyName} placeholder="Maria's laptop" />
      <button type="button" onclick={addPasskey}>Add a passkey</button>
    {:else}
      <p class="muted">This browser does not support passkeys.</p>
    {/if}
  </section>
</div>

<style>
  section + section {
    margin-top: var(--space-md);
  }
  li {
    margin-bottom: var(--space-sm);
  }
</style>
