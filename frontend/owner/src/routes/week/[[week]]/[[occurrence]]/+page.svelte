<script lang="ts">
  import { enhance } from '$app/forms';
  import { resolve } from '$app/paths';
  import { STATE_LABELS } from '$lib/schedule';
  import { displayClock, displayDate } from '$lib/time';

  let { data, form } = $props();

  const PX_PER_MINUTE = 1.1;

  // The hours the grid shows: eight to eight, stretched to whatever the week holds.
  const bounds = $derived.by(() => {
    let first = 8 * 60;
    let last = 20 * 60;
    for (const day of data.days) {
      for (const o of day.occurrences) {
        first = Math.min(first, Math.floor(o.startMinute / 60) * 60);
        last = Math.max(last, Math.ceil(o.endMinute / 60) * 60);
      }
    }
    return { first, last };
  });

  const hours = $derived(Array.from({ length: (bounds.last - bounds.first) / 60 }, (_, i) => bounds.first + i * 60));

  // Side-by-side lanes for lessons that overlap on one day.
  function lanes<T extends { startMinute: number; endMinute: number }>(items: T[]) {
    const ends: number[] = [];
    const placed = items.map((item) => {
      let lane = ends.findIndex((end) => end <= item.startMinute);
      if (lane === -1) {
        lane = ends.length;
        ends.push(item.endMinute);
      } else {
        ends[lane] = item.endMinute;
      }
      return { item, lane };
    });
    return placed.map((p) => ({ ...p, of: Math.max(ends.length, 1) }));
  }

  const sel = $derived(data.selected);
  let panel = $state<'none' | 'cancel' | 'move' | 'makeUp'>('none');
  $effect(() => {
    void sel?.id;
    panel = 'none';
  });
</script>

<div class="page" class:with-panel={sel}>
  <section class="grid-area">
    <header class="toolbar">
      <div class="nav">
        <a
          class="button secondary"
          href={resolve('/week/[[week]]/[[occurrence]]', { week: data.previousWeek })}
          aria-label="Previous week">‹</a
        >
        <a class="button secondary" href={resolve('/week/[[week]]/[[occurrence]]', {})}>This week</a>
        <a
          class="button secondary"
          href={resolve('/week/[[week]]/[[occurrence]]', { week: data.nextWeek })}
          aria-label="Next week">›</a
        >
      </div>
      <h1>Week of {displayDate(data.week)}</h1>
      <span class="muted zone">{data.zone}</span>
    </header>

    {#if !data.hasOfferings || !data.hasSeries}
      <div class="alert info">
        {#if !data.hasOfferings}
          Nothing is on the grid yet. Start by adding <a href={resolve('/offerings')}>what you teach</a>, then your
          <a href={resolve('/clients')}>clients</a> and their standing lessons.
        {:else}
          Add a <a href={resolve('/clients')}>client</a> and their standing lesson to put lessons on the grid.
        {/if}
      </div>
    {/if}

    <div class="scroller">
      <div class="week" style:--rows={`${(bounds.last - bounds.first) * PX_PER_MINUTE}px`}>
        <div class="corner"></div>
        {#each data.days as day (day.date)}
          <div class="day-head" class:today={day.isToday}>
            {day.label}
            {#if day.closedFor}<span class="closed-tag">Closed</span>{/if}
          </div>
        {/each}

        <div class="axis">
          {#each hours as hour (hour)}
            <div class="hour" style:top={`${(hour - bounds.first) * PX_PER_MINUTE}px`}>
              {displayClock(hour).replace(':00', '')}
            </div>
          {/each}
        </div>

        {#each data.days as day (day.date)}
          <div class="column" class:closed={day.closedFor} class:today={day.isToday}>
            {#each hours as hour (hour)}
              <div class="line" style:top={`${(hour - bounds.first) * PX_PER_MINUTE}px`}></div>
            {/each}
            {#if day.closedFor}
              <div class="closure-note">{day.closedFor}</div>
            {/if}
            {#each lanes(day.occurrences) as { item: o, lane, of } (o.id)}
              <a
                class="lesson {o.state}"
                class:selected={sel?.id === o.id}
                href={resolve('/week/[[week]]/[[occurrence]]', { week: data.week, occurrence: o.id })}
                data-sveltekit-noscroll
                style:top={`${(o.startMinute - bounds.first) * PX_PER_MINUTE}px`}
                style:height={`${Math.max((o.endMinute - o.startMinute) * PX_PER_MINUTE, 22)}px`}
                style:left={`calc(${(lane / of) * 100}% + 2px)`}
                style:width={`calc(${100 / of}% - 4px)`}
              >
                <span class="time">{displayClock(o.startMinute)}</span>
                <span class="what">{o.whoLabel || o.offering}</span>
                {#if o.state !== 'scheduled'}<span class="state">{STATE_LABELS[o.state]}</span>{/if}
              </a>
            {/each}
          </div>
        {/each}
      </div>
    </div>
  </section>

  {#if sel}
    <aside class="card detail">
      <div class="detail-head">
        <h2>{sel.whoLabel || sel.offering}</h2>
        <a
          class="close"
          href={resolve('/week/[[week]]/[[occurrence]]', { week: data.week })}
          data-sveltekit-noscroll
          aria-label="Close">×</a
        >
      </div>
      <p>
        {sel.offering}<br />
        {displayDate(sel.date)}, {displayClock(sel.startMinute)}–{displayClock(sel.endMinute)}
      </p>
      {#if sel.who.some((w) => w.householdId)}
        <p class="who">
          {#each sel.who as w (w.id)}
            {#if w.householdId}
              <a href={resolve('/clients/[id]', { id: w.householdId })}>{w.name}</a>
            {:else}
              <span>{w.name}</span>
            {/if}
          {/each}
        </p>
      {/if}
      <p>
        <span class="badge {sel.state}">{STATE_LABELS[sel.state]}</span>
        {#if sel.state === 'cancelled' && sel.cancelledBy}
          <span class="muted">by {sel.cancelledBy}{sel.insideNotice ? ', late' : ''}</span>
        {/if}
        {#if sel.oneOff && sel.replaces}<span class="muted">make-up</span>{/if}
      </p>
      {#if sel.reason}<p class="muted">“{sel.reason}”</p>{/if}

      {#if form?.error}
        <div class="alert error">{form.error}</div>
      {/if}

      {#if sel.state === 'scheduled'}
        <div class="actions">
          <form method="POST" action="?/complete" use:enhance>
            <input type="hidden" name="id" value={sel.id} />
            <button type="submit">Completed</button>
          </form>
          <form method="POST" action="?/noShow" use:enhance>
            <input type="hidden" name="id" value={sel.id} />
            <button type="submit" class="secondary">No-show</button>
          </form>
          <button type="button" class="secondary" onclick={() => (panel = panel === 'cancel' ? 'none' : 'cancel')}>
            Cancel…
          </button>
          <button type="button" class="secondary" onclick={() => (panel = panel === 'move' ? 'none' : 'move')}>
            Move…
          </button>
        </div>

        {#if panel === 'cancel'}
          <form class="sub" method="POST" action="?/cancel" use:enhance>
            <input type="hidden" name="id" value={sel.id} />
            <fieldset>
              <legend>Whose decision?</legend>
              <label class="inline"><input type="radio" name="by" value="household" checked /> The family</label>
              <label class="inline"><input type="radio" name="by" value="studio" /> The studio</label>
            </fieldset>
            <label for="cancel-reason">Reason (optional)</label>
            <input id="cancel-reason" name="reason" />
            <button type="submit" class="danger">Cancel this lesson</button>
          </form>
        {/if}

        {#if panel === 'move'}
          <form class="sub" method="POST" action="?/move" use:enhance>
            <input type="hidden" name="id" value={sel.id} />
            <div class="row">
              <div>
                <label for="move-date">Date</label>
                <input id="move-date" type="date" name="date" value={sel.date} required />
              </div>
              <div>
                <label for="move-time">Time</label>
                <input
                  id="move-time"
                  type="time"
                  name="time"
                  value={`${String(Math.floor(sel.startMinute / 60)).padStart(2, '0')}:${String(sel.startMinute % 60).padStart(2, '0')}`}
                  required
                />
              </div>
            </div>
            <label for="move-reason">Reason (optional)</label>
            <input id="move-reason" name="reason" />
            <button type="submit">Move</button>
          </form>
        {/if}
      {:else if sel.state !== 'moved'}
        <div class="actions">
          <form method="POST" action="?/revert" use:enhance>
            <input type="hidden" name="id" value={sel.id} />
            <button type="submit" class="secondary">Revert to scheduled</button>
          </form>
          {#if sel.state === 'cancelled'}
            <button type="button" onclick={() => (panel = panel === 'makeUp' ? 'none' : 'makeUp')}>
              Schedule make-up…
            </button>
          {/if}
        </div>

        {#if panel === 'makeUp'}
          <form class="sub" method="POST" action="?/makeUp" use:enhance>
            <input type="hidden" name="id" value={sel.id} />
            <div class="row">
              <div>
                <label for="mu-date">Date</label>
                <input id="mu-date" type="date" name="date" value={sel.date} required />
              </div>
              <div>
                <label for="mu-time">Time</label>
                <input id="mu-time" type="time" name="time" required />
              </div>
            </div>
            <label for="mu-reason">Note (optional)</label>
            <input id="mu-reason" name="reason" />
            <button type="submit">Schedule make-up</button>
          </form>
        {/if}
      {:else}
        <p class="muted">This lesson was moved; select its new slot on the grid to act on it.</p>
      {/if}

      <h3>History</h3>
      {#if data.historyError}
        <p class="muted">{data.historyError}</p>
      {:else if data.history.length === 0}
        <p class="muted">Nothing has happened to this lesson yet.</p>
      {:else}
        <ul class="history">
          {#each data.history as entry (entry.id)}
            <li>
              <strong>{entry.what}</strong>
              <span class="muted">{new Date(entry.at).toLocaleString()}</span>
              {#if entry.reason}<div class="muted">“{entry.reason}”</div>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    </aside>
  {/if}
</div>

<style>
  .page {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-md);
    padding: var(--space-md);
  }
  .page.with-panel {
    grid-template-columns: minmax(0, 1fr) 22rem;
  }
  @media (max-width: 900px) {
    .page.with-panel {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  .toolbar {
    display: flex;
    align-items: center;
    gap: var(--space-md);
    flex-wrap: wrap;
    margin-bottom: var(--space-md);
  }
  .toolbar h1 {
    margin: 0;
    font-size: 1.25rem;
  }
  .nav {
    display: flex;
    gap: 0.25rem;
  }
  .zone {
    margin-left: auto;
    font-size: 0.85rem;
  }

  .scroller {
    overflow-x: auto;
    border: 1px solid var(--color-line);
    border-radius: var(--radius);
    background: var(--color-surface);
  }
  .week {
    display: grid;
    grid-template-columns: 3.5rem repeat(7, minmax(7rem, 1fr));
    grid-template-rows: auto var(--rows);
    min-width: 52rem;
  }
  .corner,
  .day-head {
    border-bottom: 1px solid var(--color-line);
    padding: var(--space-sm);
    font-weight: 600;
    font-size: 0.9rem;
    position: sticky;
    top: 0;
    background: var(--color-surface);
  }
  .day-head.today {
    color: var(--color-accent);
  }
  .closed-tag {
    margin-left: 0.25rem;
    font-size: 0.75rem;
    color: var(--color-danger);
    font-weight: 500;
  }
  .axis {
    position: relative;
  }
  .hour {
    position: absolute;
    right: 0.4rem;
    transform: translateY(-0.5em);
    font-size: 0.75rem;
    color: var(--color-muted);
  }
  .hour:first-child {
    transform: none;
  }
  .column {
    position: relative;
    border-left: 1px solid var(--color-line);
  }
  .column.today {
    background: color-mix(in srgb, var(--color-accent) 4%, transparent);
  }
  .column.closed {
    background: repeating-linear-gradient(
      135deg,
      transparent 0 8px,
      color-mix(in srgb, var(--color-danger) 6%, transparent) 8px 16px
    );
  }
  .closure-note {
    position: absolute;
    inset: 0.25rem 0.25rem auto;
    font-size: 0.8rem;
    color: var(--color-danger);
  }
  .line {
    position: absolute;
    left: 0;
    right: 0;
    border-top: 1px dashed var(--color-line);
  }

  .lesson {
    position: absolute;
    overflow: hidden;
    border-radius: 6px;
    padding: 2px 6px;
    font-size: 0.78rem;
    line-height: 1.25;
    text-decoration: none;
    color: var(--color-accent-text);
    background: var(--color-accent);
    border: 1px solid var(--color-accent);
    display: flex;
    flex-direction: column;
  }
  .lesson .time {
    font-weight: 600;
  }
  .lesson .state {
    font-size: 0.7rem;
    opacity: 0.9;
  }
  .lesson.completed {
    background: color-mix(in srgb, var(--color-accent) 55%, white);
  }
  .lesson.cancelled,
  .lesson.moved {
    background: var(--color-surface);
    color: var(--color-muted);
    border-style: dashed;
    border-color: var(--color-muted);
    text-decoration: line-through;
  }
  .lesson.no_show {
    background: var(--color-danger-bg);
    color: var(--color-danger);
    border-color: var(--color-danger);
  }
  .lesson.selected {
    outline: 3px solid color-mix(in srgb, var(--color-accent) 40%, transparent);
    z-index: 1;
  }

  .detail {
    padding: var(--space-md);
    align-self: start;
    position: sticky;
    top: var(--space-md);
  }
  .detail-head {
    display: flex;
    justify-content: space-between;
    align-items: start;
  }
  .detail h2 {
    margin-bottom: var(--space-sm);
  }
  .detail p {
    margin: 0 0 var(--space-sm);
  }
  .close {
    text-decoration: none;
    font-size: 1.5rem;
    line-height: 1;
    color: var(--color-muted);
  }
  .who {
    display: flex;
    gap: var(--space-sm);
    flex-wrap: wrap;
  }
  .badge {
    display: inline-block;
    padding: 0 0.5rem;
    border-radius: 999px;
    font-size: 0.8rem;
    background: var(--color-info-bg);
    color: var(--color-accent);
  }
  .badge.cancelled,
  .badge.no_show {
    background: var(--color-danger-bg);
    color: var(--color-danger);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-sm);
    margin: var(--space-md) 0;
  }
  .actions form {
    margin: 0;
  }
  .sub {
    border-top: 1px solid var(--color-line);
    padding-top: var(--space-md);
    margin-bottom: var(--space-md);
  }
  fieldset {
    border: none;
    padding: 0;
    margin: 0 0 var(--space-md);
  }
  legend {
    font-weight: 500;
    margin-bottom: var(--space-sm);
  }
  label.inline {
    display: flex;
    align-items: center;
    gap: var(--space-sm);
    font-weight: 400;
  }
  label.inline input {
    width: auto;
    margin: 0;
  }
  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-sm);
  }
  h3 {
    font-size: 1rem;
    margin: var(--space-md) 0 var(--space-sm);
  }
  .history {
    list-style: none;
    padding: 0;
    margin: 0;
    font-size: 0.9rem;
  }
  .history li + li {
    margin-top: var(--space-sm);
  }
  .history .muted {
    margin-left: 0.25rem;
    font-size: 0.8rem;
  }
</style>
