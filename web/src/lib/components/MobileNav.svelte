<script lang="ts">
	import { page } from '$app/state';
	import { Film, Gamepad2, Image as ImageIcon, Tags, Settings } from '@lucide/svelte';
	import { cn } from '$lib/utils';
	import { t } from '$lib/i18n/index.svelte';

	let { openSettings }: { openSettings: () => void } = $props();

	const items = [
		{ label: 'nav.clips', href: '/', icon: Film, match: (p: string) => p === '/' || p.startsWith('/clip/') },
		{ label: 'nav.games', href: '/games', icon: Gamepad2, match: (p: string) => p.startsWith('/games') },
		{ label: 'nav.screenshots', href: '/screenshots', icon: ImageIcon, match: (p: string) => p.startsWith('/screenshots') },
		{ label: 'nav.tags', href: '/tags', icon: Tags, match: (p: string) => p.startsWith('/tags') }
	] as const;
</script>

<nav
	class="fixed inset-x-0 bottom-0 z-30 flex items-stretch border-t border-border bg-background/95 backdrop-blur md:hidden"
	style="padding-bottom: env(safe-area-inset-bottom)"
	aria-label={t('nav.main')}
>
	{#each items as item (item.href)}
		{@const active = item.match(page.url.pathname)}
		<a
			href={item.href}
			class={cn(
				'flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] font-medium transition-colors',
				active ? 'text-primary' : 'text-muted-foreground'
			)}
			aria-current={active ? 'page' : undefined}
		>
			<item.icon class="size-5" />
			{t(item.label)}
		</a>
	{/each}
	<button
		type="button"
		onclick={openSettings}
		class="flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] font-medium text-muted-foreground"
	>
		<Settings class="size-5" />
		{t('nav.settings')}
	</button>
</nav>
