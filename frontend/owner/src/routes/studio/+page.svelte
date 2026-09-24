<script lang="ts">
  import { enhance } from '$app/forms';
  import { displayDate } from '$lib/time';

  let { data, form } = $props();
</script>

<div class="wide">
  <h1>{data.studioName || 'Your studio'}</h1>

  {#if form?.error}
    <div class="alert error">{form.error}</div>
  {/if}

  <section class="card">
    <h2>Rules</h2>
    {#if form?.saved === 'settings'}<div class="alert info">Saved.</div>{/if}
    <form
      method="POST"
      action="?/settings"
      use:enhance={() =>
        ({ update }) =>
          update({ reset: false })}
    >
      <div class="row">
        <div>
          <label for="noticeHours">Cancellation notice (hours)</label>
          <input id="noticeHours" name="noticeHours" type="number" min="0" value={data.noticeHours} />
          <p class="muted hint">A family cancelling with less notice than this is marked late.</p>
        </div>
        <div>
          <label for="horizonWeeks">Plan ahead (weeks)</label>
          <input id="horizonWeeks" name="horizonWeeks" type="number" min="1" max="52" value={data.horizonWeeks} />
          <p class="muted hint">How far ahead standing slots are put on the grid.</p>
        </div>
      </div>
      <button type="submit">Save</button>
    </form>
  </section>

  <section class="card">
    <h2>Closures</h2>
    <p class="muted">
      Closing the studio cancels every lesson standing on those days and keeps new ones off them. Removing a closure
      lets future lessons back on, but lessons it already cancelled stay cancelled until you revert them.
    </p>
    {#if data.closures.length > 0}
      <ul class="closures">
        {#each data.closures as closure (closure.id)}
          <li class:past={closure.past}>
            <span>
              <strong>
                {displayDate(closure.startsOn)}{#if closure.endsOn !== closure.startsOn}
                  – {displayDate(closure.endsOn)}{/if}
              </strong>
              {#if closure.reason}<span class="muted">· {closure.reason}</span>{/if}
            </span>
            <form method="POST" action="?/reopen" use:enhance>
              <input type="hidden" name="id" value={closure.id} />
              <button type="submit" class="secondary">Remove</button>
            </form>
          </li>
        {/each}
      </ul>
    {/if}
    <form method="POST" action="?/close" use:enhance>
      <div class="row three">
        <div>
          <label for="startsOn">From</label>
          <input id="startsOn" name="startsOn" type="date" value={data.today} required />
        </div>
        <div>
          <label for="endsOn">Through</label>
          <input id="endsOn" name="endsOn" type="date" />
        </div>
        <div>
          <label for="reason">Reason</label>
          <input id="reason" name="reason" placeholder="Thanksgiving" />
        </div>
      </div>
      <button type="submit" class="danger">Close the studio</button>
    </form>
  </section>
</div>

<style>
  section + section {
    margin-top: var(--space-md);
  }
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
  .hint {
    margin: calc(-1 * var(--space-sm)) 0 var(--space-md);
    font-size: 0.85rem;
  }
  .closures {
    list-style: none;
    padding: 0;
    margin: 0 0 var(--space-md);
  }
  .closures li {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-sm) 0;
    border-bottom: 1px solid var(--color-line);
  }
  .closures li.past {
    opacity: 0.6;
  }
  .closures form {
    margin: 0;
  }
</style>
