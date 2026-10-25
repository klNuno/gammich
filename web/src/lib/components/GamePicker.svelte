<script lang="ts">
	import { Check, Plus } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import GameCover from './GameCover.svelte';
	import { createCategory, type Category } from '#lib/api.ts';
	import { categories } from '#lib/state/categories.svelte.ts';
	import { gameName } from '#lib/utils.ts';
	import { formatNumber, t } from '#lib/i18n/index.svelte.ts';

	// Filterable game list, with on-the-fly creation. Popover content: the
	// parent provides the trigger and closes the popover in onPick.
	type Props = { current?: number; onPick: (c: Category) => void };
	let { current, onPick }: Props = $props();

	let query = $state('');
	let busy = $state(false);

	const q = $derived(query.trim().toLowerCase());
	const all = $derived(
		categories.list.toSorted((a, b) => (b.latest_mtime ?? 0) - (a.latest_mtime ?? 0))
	);
	const matches = $derived(
		(q ? all.filter((c) => gameName(c.name).toLowerCase().includes(q)) : all).slice(0, 40)
	);
	const exact = $derived(all.some((c) => gameName(c.name).toLowerCase() === q));

	async function create() {
		const name = query.trim();
		if (!name || busy) return;
		busy = true;
		try {
			const c = await createCategory(name);
			await categories.load(true);
			onPick(c);
		} catch (e) {
			toast.error(t('gamePicker.createFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}

	function onKey(e: KeyboardEvent) {
		if (e.key !== 'Enter') return;
		e.preventDefault();
		if (matches.length > 0 && (exact || matches.length === 1)) onPick(matches[0]);
		else if (q) create();
	}
</script>

<div class="w-72">
	<!-- svelte-ignore a11y_autofocus -->
	<input
		bind:value={query}
		onkeydown={onKey}
		autofocus
		placeholder={t('gamePicker.search')}
		aria-label={t('gamePicker.search')}
		class="mb-1 h-9 w-full rounded-md border border-input bg-background px-2.5 text-sm outline-none focus:border-ring"
	/>
	<div class="max-h-72 overflow-y-auto">
		{#each matches as c (c.id)}
			<button
				type="button"
				onclick={() => onPick(c)}
				class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-accent"
			>
				<GameCover category={c} mosaic={1} class="aspect-[2/3] w-5 shrink-0 rounded-sm" />
				<span class="min-w-0 flex-1 truncate">{gameName(c.name)}</span>
				{#if c.id === current}
					<Check class="size-4 text-primary" />
				{:else}
					<span class="text-xs tabular-nums text-muted-foreground">{formatNumber(c.clip_count ?? 0)}</span>
				{/if}
			</button>
		{/each}
		{#if q && !exact}
			<button
				type="button"
				onclick={create}
				disabled={busy}
				class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left hover:bg-accent disabled:opacity-50"
			>
				<Plus class="size-4 text-primary" />
				<span class="truncate">{t('common.create', { name: query.trim() })}</span>
			</button>
		{:else if matches.length === 0}
			<p class="px-2 py-3 text-sm text-muted-foreground">{t('gamePicker.none')}</p>
		{/if}
	</div>
</div>
