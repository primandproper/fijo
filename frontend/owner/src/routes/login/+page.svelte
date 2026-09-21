<script lang="ts">
  import { browser } from '$app/environment';
  import { resolve } from '$app/paths';
  import { signInWithPasskey } from '$lib/passkeys';

  let { data, form } = $props();

  const supportsPasskeys = browser && typeof PublicKeyCredential !== 'undefined';
  let passkeyError = $state('');

  async function passkey() {
    passkeyError = '';
    const username = (document.getElementById('username') as HTMLInputElement | null)?.value.trim() ?? '';
    try {
      window.location.href = await signInWithPasskey(username);
    } catch (err) {
      passkeyError = err instanceof Error ? err.message : 'Passkey sign-in failed';
    }
  }
</script>

<div class="narrow">
  <div class="card">
    <h1>Sign in</h1>

    {#if data.reset}
      <div class="alert info">Your password has been reset. Sign in with the new one.</div>
    {/if}
    {#if form?.error}
      <div class="alert error">{form.error}</div>
    {/if}
    {#if passkeyError}
      <div class="alert error">{passkeyError}</div>
    {/if}

    <form method="POST" action="?/login">
      <label for="username">Username</label>
      <input id="username" name="username" autocomplete="username webauthn" value={form?.username ?? ''} required />

      <label for="password">Password</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required />

      <button type="submit">Sign in</button>
    </form>

    {#if supportsPasskeys}
      <p class="muted">or</p>
      <button class="secondary" type="button" onclick={passkey}>Sign in with a passkey</button>
    {/if}

    <p><a href={resolve('/forgot-password')}>Forgot your password?</a></p>
  </div>
</div>
