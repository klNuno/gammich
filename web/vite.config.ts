import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: {
		port: 5173,
		proxy: {
			'/api': {
				target: process.env.GAMMICH_API ?? 'http://127.0.0.1:8080',
				changeOrigin: false
			}
		}
	}
});
