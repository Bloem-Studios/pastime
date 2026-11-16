import { realpathSync } from 'node:fs';
import { defineConfig, mergeConfig } from 'vite';
import appConfig from '../../web/vite.config';

// This shared worktree uses a node_modules symlink; permit its font assets in this fixture server.
export default defineConfig(mergeConfig(appConfig, { server: { port: 18172, fs: { allow: [process.cwd(), realpathSync('node_modules')] } } }));
