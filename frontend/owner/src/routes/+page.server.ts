import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

// The grid is the owner's home.
export const load: PageServerLoad = () => {
  throw redirect(302, '/week');
};
