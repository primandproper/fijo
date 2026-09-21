import path from 'node:path';
import { includeIgnoreFile } from '@eslint/compat';
import { defineConfig } from 'eslint/config';
import { createSvelteKitConfig } from '@fijo/eslint-config';
import svelteConfig from './svelte.config.js';

const gitignorePath = path.resolve(import.meta.dirname, '../.gitignore');

export default defineConfig(includeIgnoreFile(gitignorePath), ...createSvelteKitConfig(svelteConfig));
