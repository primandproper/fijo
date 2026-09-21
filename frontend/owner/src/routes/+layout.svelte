<script lang="ts">
  import '../app.css';
  import { resolve } from '$app/paths';

  let { children } = $props();
</script>

<div class="layout">
  <header>
    <a class="brand" href={resolve('/')}>Fijo</a>
    <nav>
      <a href={resolve('/logout')}>Sign out</a>
    </nav>
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
</style>
