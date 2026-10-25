<script lang="ts">
	import { page } from '$app/state';
	import { Film, Gamepad2, Image as ImageIcon, Tags, Settings } from '@lucide/svelte';
	import { cn, gameName } from '#lib/utils.ts';
	import { categories } from '#lib/state/categories.svelte.ts';
	import GameCover from './GameCover.svelte';
	import { formatNumber, t } from '#lib/i18n/index.svelte.ts';

	let { openSettings }: { openSettings: () => void } = $props();

	const items = [
		{ label: 'nav.clips', href: '/', icon: Film, match: (p: string) => p === '/' || p.startsWith('/clip/') },
		{ label: 'nav.games', href: '/games', icon: Gamepad2, match: (p: string) => p.startsWith('/games') },
		{ label: 'nav.screenshots', href: '/screenshots', icon: ImageIcon, match: (p: string) => p.startsWith('/screenshots') },
		{ label: 'nav.tags', href: '/tags', icon: Tags, match: (p: string) => p.startsWith('/tags') }
	] as const;

	const activeGame = $derived(
		page.url.pathname === '/' ? parseInt(page.url.searchParams.get('category') ?? '0', 10) || 0 : 0
	);
	const recent = $derived(categories.recent.slice(0, 14));
</script>

<aside class="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col border-r border-border bg-card/50 md:flex">
	<a href="/" class="flex h-14 items-center gap-2 px-5" aria-label={t('app.home')}>
		<span class="size-2.5 rounded-full bg-primary shadow-[0_0_10px_var(--color-primary)]"></span>
		<span class="font-display text-2xl font-bold tracking-tight">gammich</span>
	</a>

	<nav class="px-3 pb-3" aria-label={t('nav.main')}>
		{#each items as item (item.href)}
			{@const active = item.match(page.url.pathname) && !(item.href === '/' && activeGame)}
			<a
				href={item.href}
				aria-current={active ? 'page' : undefined}
				class={cn(
					'flex items-center gap-3 rounded-md px-3 py-2 text-[15px] font-medium transition-colors',
					active
						? 'bg-primary/10 text-primary ring-1 ring-inset ring-primary/25'
						: 'text-muted-foreground hover:bg-accent/60 hover:text-foreground'
				)}
			>
				<item.icon class="size-[18px]" />
				<span class="flex-1">{t(item.label)}</span>
				{#if active}<span class="size-1.5 rounded-full bg-primary"></span>{/if}
			</a>
		{/each}
	</nav>

	<div class="flex min-h-0 flex-1 flex-col border-t border-border pt-3">
		{#if recent.length > 0 || !categories.loaded}
			<div class="flex items-baseline justify-between px-5 pb-1.5">
				<span class="text-[13px] font-semibold text-muted-foreground">{t('nav.recent')}</span>
			</div>
		{/if}
		<div class="min-h-0 flex-1 overflow-y-auto px-3 pb-3">
			{#each recent as c (c.id)}
				{@const active = c.id === activeGame}
				<a
					href="/?category={c.id}"
					aria-current={active ? 'page' : undefined}
					class={cn(
						'group flex items-center gap-2.5 rounded-md px-2 py-1.5 transition-colors',
						active ? 'bg-primary/10 ring-1 ring-inset ring-primary/25' : 'hover:bg-accent/60'
					)}
				>
					<GameCover category={c} class="size-7 shrink-0 rounded" mosaic={1} />
					<span
						class={cn(
							'min-w-0 flex-1 truncate text-sm',
							active ? 'text-primary' : 'text-muted-foreground group-hover:text-foreground'
						)}>{gameName(c.name)}</span
					>
					<span class="text-xs tabular-nums text-muted-foreground/70">{formatNumber(c.clip_count ?? 0)}</span>
				</a>
			{:else}
				{#if !categories.loaded}
					{#each Array(8) as _, i (i)}
						<div class="flex items-center gap-2.5 px-2 py-1.5">
							<div class="size-7 animate-pulse rounded bg-muted"></div>
							<div class="h-3 flex-1 animate-pulse rounded bg-muted"></div>
						</div>
					{/each}
				{/if}
			{/each}
		</div>
	</div>

	<div class="border-t border-border p-3">
		<button
			type="button"
			onclick={openSettings}
			class="flex w-full items-center gap-3 rounded-md px-3 py-2 text-[15px] font-medium text-muted-foreground transition-colors hover:bg-accent/60 hover:text-foreground"
		>
			<Settings class="size-[18px]" />
			{t('nav.settings')}
		</button>
	</div>
</aside>
