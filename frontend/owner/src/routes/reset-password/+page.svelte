<script lang="ts">
  let { data, form } = $props();
</script>

<div class="narrow">
  <div class="card">
    <h1>Choose a new password</h1>
    {#if form?.error}
      <div class="alert error">{form.error}</div>
    {/if}
    <form method="POST" action="?/reset">
      <input type="hidden" name="token" value={data.token} />
      <label for="password">New password</label>
      <input id="password" name="password" type="password" autocomplete="new-password" required />
      <label for="confirm">Once more</label>
      <input id="confirm" name="confirm" type="password" autocomplete="new-password" required />
      <button type="submit">Reset password</button>
    </form>
  </div>
</div>
