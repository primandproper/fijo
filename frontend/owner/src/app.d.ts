// See https://svelte.dev/docs/kit/types#app.d.ts
declare global {
  namespace App {
    interface Locals {
      /** The access token hooks.server.ts established for this request, absent on public paths. */
      accessToken?: string;
    }
  }
}

export {};
