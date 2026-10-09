import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

// Standing lessons lived here before they were called that.
export const load: PageServerLoad = () => redirect(308, '/lessons');
