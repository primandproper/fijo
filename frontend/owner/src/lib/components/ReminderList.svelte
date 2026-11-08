<script lang="ts">
  import { resolve } from '$app/paths';
  import type { ShapedReminder } from '$lib/server/reminders';

  let { reminders, showHousehold = false }: { reminders: ShapedReminder[]; showHousehold?: boolean } = $props();

  const STATUS = { waiting: 'Waiting to send', sent: 'Sent', skipped: 'Not sent', failed: 'Failed' } as const;
</script>

{#if reminders.length === 0}
  <p class="muted">None yet.</p>
{:else}
  <ul class="reminders">
    {#each reminders as r (r.id)}
      <li>
        <div class="head">
          <span class="status {r.status}">{STATUS[r.status]}</span>
          <strong>{r.subject}</strong>
        </div>
        <div class="muted">
          To {r.contactName} &lt;{r.toAddress}&gt;
          {#if showHousehold}
            · <a href={resolve('/clients/[id]', { id: r.householdId })}>{r.householdName}</a>
          {/if}
          · {r.which} the lesson on {r.lesson}
          · {r.sent ? `sent ${r.sent}` : `written ${r.written}`}
        </div>
        {#if r.reason}
          <div class="reason">{r.reason}</div>
        {/if}
        <details>
          <summary>See the email</summary>
          <iframe title={r.subject} sandbox="" srcdoc={r.bodyHtml}></iframe>
        </details>
      </li>
    {/each}
  </ul>
{/if}

<style>
  .reminders {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .reminders li {
    padding: var(--space-sm) 0;
    border-top: 1px solid var(--color-line);
  }
  .reminders li:first-child {
    border-top: none;
  }
  .head {
    display: flex;
    gap: var(--space-sm);
    align-items: baseline;
    flex-wrap: wrap;
  }
  .status {
    font-size: 0.8rem;
    padding: 0.05rem 0.5rem;
    border-radius: 999px;
    background: var(--color-info-bg);
    white-space: nowrap;
  }
  .status.sent {
    background: var(--color-accent);
    color: var(--color-accent-text);
  }
  .status.failed {
    background: var(--color-danger-bg);
    color: var(--color-danger);
  }
  .reason {
    font-size: 0.9rem;
    color: #8a5a00;
  }
  summary {
    cursor: pointer;
    font-size: 0.9rem;
    color: var(--color-accent);
  }
  iframe {
    width: 100%;
    min-height: 16rem;
    border: 1px solid var(--color-line);
    border-radius: var(--radius);
    background: #fff;
    margin-top: var(--space-sm);
  }
</style>
