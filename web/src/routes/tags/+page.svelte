<script lang="ts">
	import { onMount } from 'svelte';
	import { Check, Pencil, Sparkles, Trash2, X } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { deleteTag, listTagsWithCounts, renameTag, type TagWithCount } from '$lib/api';
	import { search } from '$lib/state/search.svelte';
	import { session } from '$lib/state/session.svelte';
	import { tagChipClass, tagKind } from '$lib/utils';
	import { formatNumber, i18n, t, type MessageKey } from '$lib/i18n/index.svelte';

	type Mode = 'manual' | 'auto' | 'all';
	const MODES: { k: Mode; label: MessageKey }[] = [
		{ k: 'manual', label: 'tags.mine' },
		{ k: 'auto', label: 'tags.auto' },
		{ k: 'all', label: 'tags.all' }
	];

	let tags = $state.raw<TagWithCount[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let mode = $state<Mode>('manual');
	let editing = $state<number | null>(null);
	let draft = $state('');

	async function load() {
		loading = true;
		error = null;
		try {
			tags = await listTagsWithCounts('', 1000);
			// Nothing tagged by hand: the "Mine" view would be empty.
			if (!tags.some((tag) => !tag.auto)) mode = 'all';
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		search.liveQuery = '';
		load();
	});

	const filtered = $derived.by(() => {
		const q = search.liveQuery.trim().toLowerCase();
		return tags
			.filter((tag) => (mode === 'all' ? true : mode === 'auto' ? tag.auto : !tag.auto))
			.filter((tag) => !q || tag.name.toLowerCase().includes(q))
			.toSorted((a, b) => b.clip_count - a.clip_count || a.name.localeCompare(b.name, i18n.tag));
	});

	function startEdit(tag: TagWithCount) {
		editing = tag.id;
		draft = tag.name;
	}

	async function saveEdit(tag: TagWithCount) {
		const name = draft.trim();
		editing = null;
		if (!name || name === tag.name) return;
		try {
			const r = await renameTag(tag.id, name);
			tags = tags.map((x) => (x.id === tag.id ? { ...x, ...r } : x));
			window.dispatchEvent(new CustomEvent('gammich:tags-changed'));
		} catch (e) {
			toast.error(t('common.renameFailed', { error: (e as Error).message }));
		}
	}

	async function remove(tag: TagWithCount) {
		if (!confirm(t('tags.confirmDelete', { name: tag.name, count: tag.clip_count }))) return;
		try {
			await deleteTag(tag.id);
			tags = tags.filter((x) => x.id !== tag.id);
			toast.success(t('tags.deleted', { name: tag.name }));
			window.dispatchEvent(new CustomEvent('gammich:tags-changed'));
		} catch (e) {
			toast.error(t('tags.deleteFailed', { error: (e as Error).message }));
		}
	}
</script>

<svelte:head>
	<title>Tags, gammich</title>
</svelte:head>

<div class="mx-auto max-w-4xl px-4 py-5 md:px-6">
	<div class="mb-5 flex flex-wrap items-end justify-between gap-3">
		<h1 class="font-display text-4xl font-bold leading-none">Tags</h1>
		<div class="flex h-9 rounded-md bg-secondary p-0.5 text-sm" role="group" aria-label={t('tags.filter')}>
			{#each MODES as m (m.k)}
				<button
					type="button"
					aria-pressed={mode === m.k}
					onclick={() => (mode = m.k)}
					class="rounded px-3 transition-colors {mode === m.k
						? 'bg-accent text-foreground shadow-sm'
						: 'text-muted-foreground hover:text-foreground'}"
				>
					{t(m.label)}
				</button>
			{/each}
		</div>
	</div>

	{#if error}
		<div class="flex flex-col items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 p-6 text-sm">
			<p>{t('tags.loadError', { error })}</p>
			<button class="rounded-md border border-border px-3 py-1.5 hover:bg-accent" onclick={load}>{t('common.retry')}</button>
		</div>
	{:else if loading}
		<div class="space-y-1">
			{#each Array(8) as _, i (i)}
				<div class="h-11 animate-pulse rounded-md bg-muted/60"></div>
			{/each}
		</div>
	{:else if filtered.length === 0}
		<p class="py-16 text-center text-sm text-muted-foreground">
			{#if search.liveQuery}
				{t('tags.noMatch', { query: search.liveQuery })}
			{:else if mode === 'manual'}
				{t('tags.emptyMine')}
			{:else}
				{t('tags.empty')}
			{/if}
		</p>
	{:else}
		<ul class="divide-y divide-border rounded-lg border border-border bg-card">
			{#each filtered as tag (tag.id)}
				<li class="group flex h-12 items-center gap-3 px-3">
					{#if editing === tag.id}
						<form
							class="flex flex-1 items-center gap-1.5"
							onsubmit={(e) => {
								e.preventDefault();
								saveEdit(tag);
							}}
						>
							<!-- svelte-ignore a11y_autofocus -->
							<input
								bind:value={draft}
								autofocus
								aria-label={t('tags.newName')}
								onkeydown={(e) => e.key === 'Escape' && (editing = null)}
								class="h-8 min-w-0 flex-1 rounded-md border border-input bg-background px-2 text-sm outline-none focus:border-ring"
							/>
							<button type="submit" aria-label={t('common.save')} class="grid size-8 place-items-center rounded-md bg-primary text-primary-foreground">
								<Check class="size-4" />
							</button>
							<button type="button" aria-label={t('common.cancel')} onclick={() => (editing = null)} class="grid size-8 place-items-center rounded-md hover:bg-accent">
								<X class="size-4" />
							</button>
						</form>
					{:else}
						<a href="/?tags={tag.id}" class="group/tag flex min-w-0 flex-1 items-center text-sm">
							<span
								class="inline-flex h-7 max-w-full items-center gap-1.5 rounded-md px-2.5 font-semibold ring-1 ring-inset transition-[filter] group-hover/tag:brightness-125 {tagChipClass(tag)}"
							>
								{#if tagKind(tag) === 'allstar'}<Sparkles class="size-3.5 shrink-0" />{/if}
								<span class="truncate">{tag.name}</span>
							</span>
						</a>
						<span class="text-sm tabular-nums text-muted-foreground">{formatNumber(tag.clip_count)}</span>
						{#if !tag.auto && session.canEdit}
							<div class="flex transition-opacity md:opacity-0 md:focus-within:opacity-100 md:group-hover:opacity-100">
								<button
									type="button"
									onclick={() => startEdit(tag)}
									aria-label={t('tags.renameAria', { name: tag.name })}
									title={t('common.rename')}
									class="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
								>
									<Pencil class="size-4" />
								</button>
								<button
									type="button"
									onclick={() => remove(tag)}
									aria-label={t('tags.deleteAria', { name: tag.name })}
									title={t('common.delete')}
									class="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-destructive/15 hover:text-destructive"
								>
									<Trash2 class="size-4" />
								</button>
							</div>
						{/if}
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>
