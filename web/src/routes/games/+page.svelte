<script lang="ts">
	import { onMount } from 'svelte';
	import { Eye, EyeOff, Plus } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import GameCover from '$lib/components/GameCover.svelte';
	import { createCategory, type Category } from '$lib/api';
	import { categories } from '$lib/state/categories.svelte';
	import { session } from '$lib/state/session.svelte';
	import { search } from '$lib/state/search.svelte';
	import { formatDateRelative, gameName } from '$lib/utils';
	import { i18n, t, type MessageKey } from '$lib/i18n/index.svelte';

	type Order = 'recent' | 'count' | 'name';
	const ORDERS: { k: Order; label: MessageKey }[] = [
		{ k: 'recent', label: 'games.recent' },
		{ k: 'count', label: 'games.mostClips' },
		{ k: 'name', label: 'games.name' }
	];

	let order = $state<Order>('recent');
	let showHidden = $state(false);
	let creating = $state(false);
	let newName = $state('');
	let busy = $state(false);

	onMount(() => {
		search.liveQuery = '';
		categories.load(true);
	});

	const hiddenCount = $derived(categories.list.filter((c) => c.hidden).length);
	const games = $derived.by(() => {
		const q = search.liveQuery.trim().toLowerCase();
		const list = categories.list.filter(
			(c) => (showHidden || !c.hidden) && (!q || gameName(c.name).toLowerCase().includes(q))
		);
		const byName = (a: Category, b: Category) => gameName(a.name).localeCompare(gameName(b.name), i18n.tag);
		if (order === 'name') return list.toSorted(byName);
		if (order === 'count') return list.toSorted((a, b) => (b.clip_count ?? 0) - (a.clip_count ?? 0) || byName(a, b));
		return list.toSorted((a, b) => (b.latest_mtime ?? 0) - (a.latest_mtime ?? 0) || byName(a, b));
	});

	async function submitCreate() {
		const name = newName.trim();
		if (!name || busy) return;
		busy = true;
		try {
			await createCategory(name);
			toast.success(t('games.added', { name }));
			newName = '';
			creating = false;
			await categories.load(true);
		} catch (e) {
			toast.error(t('games.addFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}

	async function toggleHidden(c: Category) {
		try {
			await categories.update(c.id, { hidden: !c.hidden });
			toast.success(t(c.hidden ? 'games.shown' : 'games.hidden', { name: gameName(c.name) }));
		} catch (e) {
			toast.error(t('common.updateFailed', { error: (e as Error).message }));
		}
	}
</script>

<svelte:head>
	<title>{t('nav.games')}, gammich</title>
</svelte:head>

<div class="px-4 py-5 md:px-6">
	<div class="mb-5 flex flex-wrap items-end justify-between gap-3">
		<div>
			<h1 class="font-display text-4xl font-bold leading-none">{t('nav.games')}</h1>
			{#if categories.loaded}
				<p class="mt-1.5 text-sm text-muted-foreground">
					{t('count.games', { count: games.length })}{search.liveQuery ? t('games.forQuery', { query: search.liveQuery }) : ''}
				</p>
			{/if}
		</div>

		<div class="flex flex-wrap items-center gap-2">
			<div class="flex h-9 rounded-md bg-secondary p-0.5 text-sm" role="group" aria-label={t('games.order')}>
				{#each ORDERS as o (o.k)}
					<button
						type="button"
						aria-pressed={order === o.k}
						onclick={() => (order = o.k)}
						class="rounded px-3 transition-colors {order === o.k
							? 'bg-accent text-foreground shadow-sm'
							: 'text-muted-foreground hover:text-foreground'}"
					>
						{t(o.label)}
					</button>
				{/each}
			</div>
			{#if hiddenCount > 0}
				<button
					type="button"
					aria-pressed={showHidden}
					onclick={() => (showHidden = !showHidden)}
					class="inline-flex h-9 items-center gap-2 rounded-md border px-3 text-sm transition-colors {showHidden
						? 'border-primary/50 text-foreground'
						: 'border-border text-muted-foreground hover:text-foreground'}"
				>
					<Eye class="size-4" /> {t('games.hiddenFilter', { count: hiddenCount })}
				</button>
			{/if}
			{#if session.canEdit && creating}
				<form
					class="flex items-center gap-1.5"
					onsubmit={(e) => {
						e.preventDefault();
						submitCreate();
					}}
				>
					<!-- svelte-ignore a11y_autofocus -->
					<input
						bind:value={newName}
						placeholder={t('game.nameLabel')}
						aria-label={t('game.nameLabel')}
						autofocus
						onkeydown={(e) => {
							if (e.key === 'Escape') {
								creating = false;
								newName = '';
							}
						}}
						class="h-9 w-48 rounded-md border border-input bg-background px-2.5 text-sm outline-none focus:border-ring"
					/>
					<button
						type="submit"
						disabled={busy || !newName.trim()}
						class="h-9 rounded-md bg-primary px-3 text-sm font-semibold text-primary-foreground disabled:opacity-50"
					>
						{t('games.add')}
					</button>
				</form>
			{:else if session.canEdit}
				<button
					type="button"
					onclick={() => (creating = true)}
					class="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-3 text-sm transition-colors hover:bg-accent"
				>
					<Plus class="size-4" /> {t('games.new')}
				</button>
			{/if}
		</div>
	</div>

	{#if categories.error && !categories.loaded}
		<div class="flex flex-col items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 p-6 text-sm">
			<p>{t('games.loadError', { error: categories.error })}</p>
			<button class="rounded-md border border-border px-3 py-1.5 hover:bg-accent" onclick={() => categories.load(true)}>
				{t('common.retry')}
			</button>
		</div>
	{:else if !categories.loaded}
		<div class="grid grid-cols-3 gap-x-4 gap-y-6 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8">
			{#each Array(16) as _, i (i)}
				<div class="aspect-[2/3] animate-pulse rounded-md bg-muted"></div>
			{/each}
		</div>
	{:else if games.length === 0}
		<p class="py-16 text-center text-sm text-muted-foreground">
			{search.liveQuery ? t('games.noMatch', { query: search.liveQuery }) : t('games.empty')}
		</p>
	{:else}
		<div class="grid grid-cols-3 gap-x-4 gap-y-6 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-6 xl:grid-cols-8">
			{#each games as g (g.id)}
				<div class="group relative {g.hidden ? 'opacity-50' : ''}">
					<a href="/?category={g.id}" class="block rounded-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
						<GameCover
							category={g}
							class="aspect-[2/3] w-full rounded-md ring-1 ring-white/5 transition-shadow group-hover:ring-2 group-hover:ring-primary"
						/>
						<div class="mt-2 line-clamp-2 text-sm font-medium leading-snug">{gameName(g.name)}</div>
						<div class="mt-0.5 text-xs text-muted-foreground">
							{#if g.clip_count}
								<span class="tabular-nums">{t('count.clips', { count: g.clip_count })}</span>{#if g.latest_mtime},
									{formatDateRelative(g.latest_mtime)}{/if}
							{:else}
								{t('games.noClips')}
							{/if}
						</div>
					</a>
					{#if session.canEdit}
						<button
							type="button"
							onclick={() => toggleHidden(g)}
							title={g.hidden ? t('games.showTitle') : t('games.hideTitle')}
							aria-label={t(g.hidden ? 'games.showAria' : 'games.hideAria', { name: gameName(g.name) })}
							class="absolute right-1.5 top-1.5 grid size-8 place-items-center rounded-md bg-black/65 text-white transition-opacity hover:bg-black/85 md:opacity-0 md:focus-visible:opacity-100 md:group-hover:opacity-100"
						>
							{#if g.hidden}<Eye class="size-4" />{:else}<EyeOff class="size-4" />{/if}
						</button>
					{/if}
				</div>
			{/each}
		</div>
	{/if}
</div>
