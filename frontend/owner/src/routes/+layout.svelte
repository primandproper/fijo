<script lang="ts">
  import '../app.css';
  import { resolve } from '$app/paths';
  import { page } from '$app/state';

  let { children } = $props();

  const PUBLIC = ['/login', '/forgot-password', '/reset-password'];
  const signedIn = $derived(!PUBLIC.some((p) => page.url.pathname.startsWith(p)));
  const links = [
    { href: '/week', label: 'Week' },
    { href: '/clients', label: 'Clients' },
    { href: '/series', label: 'Lessons' },
    { href: '/offerings', label: 'Offerings' },
    { href: '/studio', label: 'Studio' },
    { href: '/account', label: 'Account' },
  ] as const;
</script>

<div class="layout">
  <header>
    <a class="brand" href={resolve('/')}>Fijo</a>
    {#if signedIn}
      <nav>
        {#each links as link (link.href)}
          <a href={resolve(link.href)} class:current={page.url.pathname.startsWith(link.href)}>{link.label}</a>
        {/each}
        <a href={resolve('/logout')}>Sign out</a>
      </nav>
    {/if}
  </header>
  <main>
    {@render children()}
  </main>
</div>

<style>
  .layout {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-md) var(--space-lg);
    gap: var(--space-md);
    flex-wrap: wrap;
    border-bottom: 1px solid var(--color-line);
    background: var(--color-surface);
  }
  .brand {
    font-weight: 700;
    text-decoration: none;
    color: var(--color-text);
  }
  main {
    flex: 1;
  }
  nav {
    display: flex;
    gap: var(--space-md);
    flex-wrap: wrap;
  }
  nav a {
    text-decoration: none;
    color: var(--color-muted);
  }
  nav a.current {
    color: var(--color-accent);
    font-weight: 600;
  }
</style>
