<script lang="ts">
  import { resolve } from '$app/paths';

  let { form } = $props();
</script>

<div class="narrow">
  <div class="card">
    <h1>Forgot your password?</h1>
    {#if form?.sent}
      <div class="alert info">If that address belongs to an account, a reset link is on its way.</div>
    {:else}
      {#if form?.error}
        <div class="alert error">{form.error}</div>
      {/if}
      <form method="POST" action="?/request">
        <label for="emailAddress">Email address</label>
        <input id="emailAddress" name="emailAddress" type="email" autocomplete="email" required />
        <button type="submit">Send a reset link</button>
      </form>
    {/if}
    <p><a href={resolve('/login')}>Back to sign in</a></p>
  </div>
</div>
