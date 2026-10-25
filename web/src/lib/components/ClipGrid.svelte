<script lang="ts" module>
	import type { Clip } from '#lib/api.ts';

	type CacheEntry = {
		clips: Clip[];
		total: number;
		pageNum: number;
		scrollY: number;
		at: number;
	};
	// Restores list + scroll when coming back to a page (back from /clip/[id], tab change).
	const gridCache = new Map<string, CacheEntry>();
	const CACHE_TTL = 5 * 60 * 1000;
	const CACHE_MAX = 6;
</script>

<script lang="ts">
	import { page } from '$app/state';
	import { goto, replaceState } from '$app/navigation';
	import { tick } from 'svelte';
	import { SearchX, Clapperboard, X, Tag as TagIcon, ArrowDownUp, CheckSquare } from '@lucide/svelte';
	import ClipCard from '#lib/components/ClipCard.svelte';
	import ClipDialog from '#lib/components/ClipDialog.svelte';
	import BulkActionBar from '#lib/components/BulkActionBar.svelte';
	import GameHeader from '#lib/components/GameHeader.svelte';
	import GameStrip from '#lib/components/GameStrip.svelte';
	import { listClips, SORT_OPTIONS, sortLabel as labelOf, type SortOption } from '#lib/api.ts';
	import { t } from '#lib/i18n/index.svelte.ts';
	import { dateGroupLabel } from '#lib/utils.ts';
	import { ui, DENSITY_GRID } from '#lib/state/ui.svelte.ts';
	import { selection } from '#lib/state/selection.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';

	type Props = {
		kind?: 'video' | 'screenshot';
		emptyLabel?: string;
	};

	let { kind, emptyLabel }: Props = $props();

	let clips: Clip[] = $state([]);
	let total = $state(0);
	let pageNum = $state(1);
	const perPage = 60;
	let loading = $state(true);
	let loadingMore = $state(false);
	let error = $state<string | null>(null);
	let moreError = $state<string | null>(null);
	let sentinel: HTMLElement | null = $state(null);
	let inflight: AbortController | null = null;
	let loadedKey = $state(''); // key of the filter actually loaded in `clips`
	let lastSelectedIndex: number | null = null;

	const params = $derived(page.url.searchParams);
	const query = $derived(params.get('q') ?? '');
	const sort = $derived((params.get('sort') ?? 'date_desc') as SortOption);
	const categoryId = $derived(parseInt(params.get('category') ?? '0', 10) || 0);
	const tagKey = $derived(params.get('tags') ?? '');
	const tagFilter = $derived(
		tagKey ? tagKey.split(',').map((s) => parseInt(s, 10)).filter((n) => n > 0) : []
	);

	const clipId = $derived.by(() => {
		const st = (page.state as { clipId?: number | null } | null) ?? null;
		if (st && 'clipId' in st) return st.clipId ?? null;
		const urlId = parseInt(params.get('clip') ?? '', 10);
		return Number.isFinite(urlId) && urlId > 0 ? urlId : null;
	});

	const listKey = $derived(`${kind ?? 'video'}|${query}|${sort}|${tagKey}|${categoryId}`);

	$effect(() => {
		const key = listKey;
		// Changing the filter cancels the current selection.
		selection.reset();
		lastSelectedIndex = null;
		const cached = gridCache.get(key);
		if (cached && Date.now() - cached.at < CACHE_TTL) {
			gridCache.delete(key);
			inflight?.abort();
			inflight = null;
			clips = cached.clips;
			total = cached.total;
			pageNum = cached.pageNum;
			loadedKey = key;
			loading = false;
			error = null;
			tick().then(() => window.scrollTo(0, cached.scrollY));
			return;
		}
		load(key);
	});

	$effect(() => {
		return () => {
			inflight?.abort();
			selection.reset();
			// Cache ONLY when the list matches the current filter (not a load still
			// in flight): otherwise the cache gets poisoned with the old list.
			if (clips.length > 0 && !error && !loading && loadedKey === listKey) {
				if (gridCache.size >= CACHE_MAX) {
					const oldest = gridCache.keys().next().value;
					if (oldest !== undefined) gridCache.delete(oldest);
				}
				gridCache.set(listKey, {
					clips,
					total,
					pageNum,
					scrollY: window.scrollY,
					at: Date.now()
				});
			}
		};
	});

	$effect(() => {
		if (!sentinel) return;
		const el = sentinel;
		const observer = new IntersectionObserver(
			(entries) => {
				if (entries[0].isIntersecting && !loading && !loadingMore && clips.length < total) {
					loadMore();
				}
			},
			{ rootMargin: '400px 0px' }
		);
		observer.observe(el);
		return () => observer.disconnect();
	});

	async function load(key: string) {
		inflight?.abort();
		const ctrl = new AbortController();
		inflight = ctrl;
		loading = true;
		error = null;
		moreError = null;
		try {
			const r = await listClips({
				q: query || undefined,
				sort,
				tags: tagFilter,
				category: categoryId || undefined,
				kind,
				page: 1,
				per_page: perPage,
				signal: ctrl.signal
			});
			if (key !== listKey) return;
			clips = r.clips ?? [];
			total = r.total;
			pageNum = 1;
			loadedKey = key;
		} catch (e) {
			if ((e as Error).name === 'AbortError' || key !== listKey) return;
			error = (e as Error).message;
			clips = [];
			total = 0;
		} finally {
			if (inflight === ctrl) {
				loading = false;
				inflight = null;
			}
		}
	}

	async function loadMore() {
		if (loading || loadingMore || clips.length >= total) return;
		const key = listKey;
		const nextPage = pageNum + 1;
		loadingMore = true;
		moreError = null;
		try {
			const r = await listClips({
				q: query || undefined,
				sort,
				tags: tagFilter,
				category: categoryId || undefined,
				kind,
				page: nextPage,
				per_page: perPage
			});
			if (key !== listKey) return;
			const seen = new Set(clips.map((c) => c.id));
			clips = [...clips, ...(r.clips ?? []).filter((c) => !seen.has(c.id))];
			total = r.total;
			pageNum = nextPage;
			// A13: if the sentinel stays in the viewport (big screen, short page),
			// the IntersectionObserver does not fire again, so retry by hand.
			await tick();
			if (sentinel && clips.length < total) {
				const rect = sentinel.getBoundingClientRect();
				if (rect.top < window.innerHeight + 400) loadMore();
			}
		} catch (e) {
			if (key === listKey) moreError = (e as Error).message;
		} finally {
			loadingMore = false;
		}
	}

	function closeDialog() {
		const st = (history.state as { clipId?: number | null } | null) ?? null;
		const hasOurState = !!(st && typeof st === 'object' && 'clipId' in st && st.clipId);
		if (hasOurState) {
			history.back();
			return;
		}
		const p = new URLSearchParams(page.url.search);
		p.delete('clip');
		const qs = p.toString();
		replaceState(qs ? `?${qs}` : page.url.pathname, { clipId: null });
	}

	// Prev/next navigation in the dialog: replaceState so a single "back" closes
	// the dialog to the grid (no history of every clip seen).
	function navigateToClip(id: number) {
		const p = new URLSearchParams(page.url.search);
		p.set('clip', String(id));
		replaceState(`?${p.toString()}`, { clipId: id });
	}

	function onClipUpdated(updated: Clip) {
		clips = clips.map((c) => (c.id === updated.id ? updated : c));
	}

	// --- Multi-selection ---
	function onSelect(e: MouseEvent | KeyboardEvent, index: number) {
		if (e.shiftKey && lastSelectedIndex != null) {
			selection.selectRange(
				clips.map((c) => c.id),
				lastSelectedIndex,
				index
			);
		} else {
			selection.toggle(clips[index].id);
			lastSelectedIndex = index;
		}
	}

	const selectedIds = $derived([...selection.ids]);

	async function reloadAfterBulk() {
		selection.reset();
		lastSelectedIndex = null;
		gridCache.delete(listKey);
		await load(listKey);
	}

	// --- Active filters (chips) ---
	// Names resolved from the loaded clips (all of them carry the active AND filter).
	// Fallback name for the game header until the game list is there.
	const fallbackCategoryName = $derived(
		categoryId > 0 ? clips.find((c) => c.category_id === categoryId)?.category_name : undefined
	);
	const activeTagNames = $derived(
		tagFilter.map((id) => {
			for (const c of clips) {
				const t = c.tags?.find((t) => t.id === id);
				if (t) return { id, name: t.name };
			}
			return { id, name: `#${id}` };
		})
	);
	const sortLabel = $derived(
		sort !== 'date_desc' && SORT_OPTIONS.includes(sort) ? labelOf(sort) : null
	);
	const hasFilters = $derived(!!query || !!tagKey || categoryId > 0);
	const hasChips = $derived(!!query || !!tagKey || sortLabel != null);

	function setParams(mut: (p: URLSearchParams) => void) {
		const p = new URLSearchParams(page.url.search);
		mut(p);
		p.delete('clip');
		const qs = p.toString();
		goto(page.url.pathname + (qs ? `?${qs}` : ''), { reset: false });
	}
	const removeQuery = () => setParams((p) => p.delete('q'));
	const removeCategory = () => setParams((p) => p.delete('category'));
	const removeSort = () => setParams((p) => p.delete('sort'));
	function removeTag(id: number) {
		setParams((p) => {
			const rest = tagFilter.filter((t) => t !== id);
			rest.length ? p.set('tags', rest.join(',')) : p.delete('tags');
		});
	}
	function clearFilters() {
		const p = new URLSearchParams();
		const s = params.get('sort');
		if (s) p.set('sort', s);
		const qs = p.toString();
		goto(page.url.pathname + (qs ? `?${qs}` : ''), { reset: false });
	}

	// --- Date grouping (only when sorting by date) ---
	type Row = { header: string } | { clip: Clip; i: number };
	const rows = $derived.by<Row[]>(() => {
		const byDate = sort === 'date_desc' || sort === 'date_asc';
		if (!byDate) return clips.map((clip, i) => ({ clip, i }));
		const out: Row[] = [];
		let prev = '';
		clips.forEach((clip, i) => {
			const label = dateGroupLabel(clip.mtime);
			if (label !== prev) {
				out.push({ header: label.charAt(0).toUpperCase() + label.slice(1) });
				prev = label;
			}
			out.push({ clip, i });
		});
		return out;
	});
</script>

{#if categoryId > 0}
	<GameHeader {categoryId} fallbackName={fallbackCategoryName} {total} onClose={removeCategory} />
{:else if kind !== 'screenshot' && !hasFilters}
	<GameStrip />
{/if}

{#if hasChips}
	<div class="mb-4 flex flex-wrap items-center gap-1.5">
		{#if query}
			<button
				class="inline-flex h-7 items-center gap-1.5 rounded-md bg-secondary px-2.5 text-sm transition-colors hover:bg-accent"
				onclick={removeQuery}
			>
				{t('grid.queryChip', { query })} <X class="size-3.5 text-muted-foreground" />
			</button>
		{/if}
		{#each activeTagNames as t (t.id)}
			<button
				class="inline-flex h-7 items-center gap-1.5 rounded-md bg-primary/15 px-2.5 text-sm text-primary transition-colors hover:bg-primary/25"
				onclick={() => removeTag(t.id)}
			>
				<TagIcon class="size-3.5" /> {t.name} <X class="size-3.5" />
			</button>
		{/each}
		{#if sortLabel}
			<button
				class="inline-flex h-7 items-center gap-1.5 rounded-md bg-secondary px-2.5 text-sm transition-colors hover:bg-accent"
				onclick={removeSort}
			>
				<ArrowDownUp class="size-3.5 text-muted-foreground" /> {sortLabel} <X class="size-3.5 text-muted-foreground" />
			</button>
		{/if}
		<button
			class="ml-1 text-sm text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
			onclick={clearFilters}
		>
			{t('grid.clearAll')}
		</button>
	</div>
{/if}

{#if error}
	<div class="flex flex-col items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 p-6 text-sm">
		<p>{t('grid.loadError', { error })}</p>
		<button
			class="rounded-md border border-border px-3 py-1.5 font-medium transition-colors hover:bg-accent"
			onclick={() => load(listKey)}
		>
			{t('common.retry')}
		</button>
	</div>
{:else if loading && clips.length === 0}
	<div class="grid gap-4 {DENSITY_GRID[ui.density]}">
		{#each Array(12) as _, i (i)}
			<div class="rounded-xl bg-card ring-1 ring-border">
				<div class="aspect-video animate-pulse rounded-t-xl bg-muted"></div>
				<div class="p-3">
					<div class="h-3.5 w-3/4 animate-pulse rounded bg-muted"></div>
					<div class="mt-2.5 h-5 w-2/3 animate-pulse rounded bg-muted/70"></div>
				</div>
			</div>
		{/each}
	</div>
{:else if clips.length === 0}
	<div class="flex h-64 flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
		{#if hasFilters}
			<SearchX class="size-8 opacity-50" />
			<p>{t('grid.noMatch')}</p>
			<button
				class="rounded-md border border-border px-3 py-1.5 font-medium transition-colors hover:bg-accent hover:text-foreground"
				onclick={clearFilters}
			>
				{t('grid.clearFilters')}
			</button>
		{:else}
			<Clapperboard class="size-8 opacity-50" />
			<p>{emptyLabel ?? t('grid.empty')}</p>
		{/if}
	</div>
{:else}
	<div class="mb-3 flex items-center justify-between text-sm text-muted-foreground">
		<!-- On a game page, the header already gives the total. -->
		<span class="tabular-nums"
			>{categoryId > 0 ? '' : t(kind === 'screenshot' ? 'count.screenshots' : 'count.clips', { count: total })}</span
		>
		{#if session.canEdit}
			<button
				class="inline-flex h-8 items-center gap-1.5 rounded-md px-2 transition-colors hover:bg-accent hover:text-foreground {selection.active
					? 'text-foreground'
					: ''}"
				onclick={() => (selection.active ? selection.reset() : selection.enter())}
			>
				<CheckSquare class="size-4" />
				{selection.active ? t('grid.cancelSelection') : t('grid.select')}
			</button>
		{/if}
	</div>

	<div class="grid gap-4 {DENSITY_GRID[ui.density]}">
		{#each rows as row ('header' in row ? 'header:' + row.header : row.clip.id)}
			{#if 'header' in row}
				<h2 class="col-span-full -mb-1 mt-4 font-display text-xl font-semibold first:mt-0">
					{row.header}
				</h2>
			{:else}
				<div class="card-cv">
					<ClipCard clip={row.clip} index={row.i} showGame={categoryId === 0} {onSelect} />
				</div>
			{/if}
		{/each}
	</div>

	{#if clips.length < total}
		<div bind:this={sentinel} class="mt-8 flex h-16 items-center justify-center text-sm text-muted-foreground">
			{#if moreError}
				<button
					class="rounded-md border border-destructive/50 px-3 py-1.5 font-medium transition-colors hover:bg-destructive/10"
					onclick={loadMore}
				>
					{t('grid.moreError')}
				</button>
			{:else if loadingMore}
				<span class="size-5 animate-spin rounded-full border-2 border-muted-foreground/40 border-t-muted-foreground"></span>
			{/if}
		</div>
	{/if}
{/if}

{#if session.canEdit && selection.active && selectedIds.length > 0}
	<BulkActionBar ids={selectedIds} onDone={reloadAfterBulk} onCancel={() => selection.reset()} />
{/if}

<ClipDialog
	{clipId}
	onClose={closeDialog}
	{onClipUpdated}
	siblingIds={clips.map((c) => c.id)}
	onNavigate={navigateToClip}
/>
