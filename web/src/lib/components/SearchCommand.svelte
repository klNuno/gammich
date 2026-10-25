<script lang="ts">
	import { Search, Tag as TagIcon, Gamepad2, Film, X } from '@lucide/svelte';
	import { unifiedSearch, type SearchResponse, type Tag } from '#lib/api.ts';
	import { goto, pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { search } from '#lib/state/search.svelte.ts';
	import { clipTitle, gameName } from '#lib/utils.ts';
	import { formatNumber, t } from '#lib/i18n/index.svelte.ts';

	type Props = { activeTags: Tag[]; query: string };
	let { activeTags, query }: Props = $props();

	let input = $state('');
	let focused = $state(false);
	let results: SearchResponse = $state({ tags: [], categories: [], clips: [] });
	let loading = $state(false);
	let activeIndex = $state(-1);
	let debounce: ReturnType<typeof setTimeout> | null = null;
	let urlDebounce: ReturnType<typeof setTimeout> | null = null;
	let searchSeq = 0;

	// List pages filter on the server through ?q=; games/tags filter on the client through liveQuery.
	const listPage = $derived(
		page.url.pathname === '/' || page.url.pathname === '/screenshots'
	);
	const clientFilterPage = $derived(
		page.url.pathname === '/games' || page.url.pathname === '/tags'
	);

	$effect(() => {
		if (focused) return;
		input = listPage ? query : clientFilterPage ? search.liveQuery : '';
	});

	$effect(() => {
		return () => {
			if (debounce) clearTimeout(debounce);
			if (urlDebounce) clearTimeout(urlDebounce);
		};
	});

	function pushQueryToURL(v: string) {
		if (urlDebounce) clearTimeout(urlDebounce);
		urlDebounce = setTimeout(() => {
			const params = new URLSearchParams(page.url.search);
			const trimmed = v.trim();
			trimmed ? params.set('q', trimmed) : params.delete('q');
			const qs = params.toString();
			goto(page.url.pathname + (qs ? `?${qs}` : ''), {
				replace: true,
				reset: false
			});
		}, 300);
	}

	function runSearch(v: string) {
		if (clientFilterPage) {
			search.liveQuery = v;
			return;
		}
		if (listPage) pushQueryToURL(v);
		if (debounce) clearTimeout(debounce);
		activeIndex = -1;
		if (!v.trim()) {
			results = { tags: [], categories: [], clips: [] };
			loading = false;
			return;
		}
		loading = true;
		debounce = setTimeout(async () => {
			const seq = ++searchSeq;
			try {
				const res = await unifiedSearch(v.trim(), 6);
				if (seq !== searchSeq) return; // stale response, ignore
				results = res;
			} catch {
				if (seq !== searchSeq) return;
				results = { tags: [], categories: [], clips: [] };
			} finally {
				if (seq === searchSeq) loading = false;
			}
		}, 130);
	}

	function onInput(e: Event) {
		const v = (e.target as HTMLInputElement).value;
		input = v;
		runSearch(v);
	}

	function applyTagFilter(tagId: number) {
		const base = listPage ? page.url.pathname : '/';
		const params = new URLSearchParams(listPage ? page.url.search : undefined);
		const cur = (params.get('tags') ?? '')
			.split(',')
			.map((s) => parseInt(s, 10))
			.filter((n) => n > 0);
		if (!cur.includes(tagId)) cur.push(tagId);
		params.set('tags', cur.join(','));
		params.delete('q');
		goto(base + '?' + params.toString(), { reset: false });
		closeDropdown();
	}

	function applyCategory(catId: number) {
		const base = listPage ? page.url.pathname : '/';
		// Same as applyTagFilter: keeps the existing params (sort, etc.), only drops q.
		const params = new URLSearchParams(listPage ? page.url.search : undefined);
		params.set('category', String(catId));
		params.delete('q');
		goto(base + '?' + params.toString(), { reset: false });
		closeDropdown();
	}

	function openClip(id: number) {
		if (listPage) {
			// Shallow routing like ClipCard, no stray history entry.
			const params = new URLSearchParams(page.url.search);
			params.set('clip', String(id));
			pushState(`?${params.toString()}`, { clipId: id });
		} else {
			goto('/?clip=' + id);
		}
		closeDropdown();
	}

	function submitQuery() {
		const v = input.trim();
		const base = listPage ? page.url.pathname : '/';
		const params = new URLSearchParams(listPage ? page.url.search : undefined);
		v ? params.set('q', v) : params.delete('q');
		goto(base + '?' + params.toString(), {
			replace: listPage,
			reset: false
		});
		closeDropdown();
	}

	function removeActiveTag(id: number) {
		const params = new URLSearchParams(page.url.search);
		const cur = (params.get('tags') ?? '')
			.split(',')
			.map((s) => parseInt(s, 10))
			.filter((n) => n > 0 && n !== id);
		if (cur.length) params.set('tags', cur.join(','));
		else params.delete('tags');
		const qs = params.toString();
		goto(qs ? '?' + qs : '?', { replace: true, reset: false });
	}

	function clearInput() {
		input = '';
		if (clientFilterPage) search.liveQuery = '';
		if (listPage) pushQueryToURL('');
		results = { tags: [], categories: [], clips: [] };
		activeIndex = -1;
	}

	function closeDropdown() {
		focused = false;
		activeIndex = -1;
		(document.activeElement as HTMLElement)?.blur?.();
	}

	type ResultItem =
		| { type: 'tag'; id: number }
		| { type: 'category'; id: number }
		| { type: 'clip'; id: number };

	const flatResults = $derived<ResultItem[]>([
		...results.tags.map((t) => ({ type: 'tag' as const, id: t.id })),
		...results.categories.map((c) => ({ type: 'category' as const, id: c.id })),
		...results.clips.map((c) => ({ type: 'clip' as const, id: c.id }))
	]);

	function pickItem(item: ResultItem) {
		if (item.type === 'tag') applyTagFilter(item.id);
		else if (item.type === 'category') applyCategory(item.id);
		else openClip(item.id);
	}

	function moveActive(delta: number) {
		if (flatResults.length === 0) return;
		activeIndex = (activeIndex + delta + flatResults.length) % flatResults.length;
		document
			.getElementById(`search-opt-${activeIndex}`)
			?.scrollIntoView({ block: 'nearest' });
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			moveActive(1);
			return;
		}
		if (e.key === 'ArrowUp') {
			e.preventDefault();
			moveActive(-1);
			return;
		}
		if (e.key === 'Enter') {
			e.preventDefault();
			if (showDropdown && activeIndex >= 0 && activeIndex < flatResults.length) {
				pickItem(flatResults[activeIndex]);
			} else {
				submitQuery();
			}
			return;
		}
		if (e.key === 'Escape') {
			clearInput();
			(e.target as HTMLInputElement).blur();
		}
	}

	function onGlobalKey(e: KeyboardEvent) {
		if (e.key !== '/') return;
		const el = document.activeElement;
		const tag = el?.tagName;
		if (tag === 'INPUT' || tag === 'TEXTAREA' || (el as HTMLElement)?.isContentEditable) return;
		if (document.querySelector('[role="dialog"]')) return;
		e.preventDefault();
		(document.querySelector('#topbar-search') as HTMLInputElement)?.focus();
	}

	let hasResults = $derived(flatResults.length > 0);
	let showDropdown = $derived(listPage && focused && input.trim().length > 0);
</script>

<svelte:window onkeydown={onGlobalKey} />

<div class="relative flex-1">
	<div
		class="flex h-10 w-full items-center gap-2 rounded-lg border border-transparent bg-muted px-3 text-[15px] transition-colors focus-within:border-input focus-within:bg-card"
	>
		<Search class="size-4 shrink-0 text-muted-foreground" />
		{#if listPage}
			{#each activeTags as tag (tag.id)}
				<button
					type="button"
					class="inline-flex items-center gap-1 rounded bg-primary/20 px-1.5 py-0.5 text-xs text-primary transition-colors hover:bg-primary/30"
					onclick={() => removeActiveTag(tag.id)}
				>
					<TagIcon class="size-3" />
					{tag.name}
					<X class="size-3" />
				</button>
			{/each}
		{/if}
		<input
			id="topbar-search"
			type="text"
			role="combobox"
			aria-expanded={showDropdown}
			aria-controls="search-results"
			aria-activedescendant={activeIndex >= 0 ? `search-opt-${activeIndex}` : undefined}
			aria-autocomplete="list"
			value={input}
			placeholder={listPage
				? activeTags.length
					? ''
					: t('search.placeholder')
				: clientFilterPage
					? t('search.filter')
					: t('search.search')}
			class="h-full min-w-0 flex-1 bg-transparent placeholder:text-muted-foreground focus:outline-none"
			oninput={onInput}
			onfocus={() => (focused = true)}
			onblur={() => setTimeout(() => (focused = false), 150)}
			onkeydown={onKey}
		/>
		{#if input}
			<button
				type="button"
				class="grid size-5 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
				onclick={clearInput}
				title={t('search.clear')}
			>
				<X class="size-3.5" />
			</button>
		{:else}
			<kbd
				class="hidden rounded border border-border bg-background px-1.5 py-0.5 text-[11px] text-muted-foreground md:inline"
				>/</kbd
			>
		{/if}
	</div>

	{#if showDropdown}
		<div
			id="search-results"
			class="absolute left-0 right-0 top-[calc(100%+4px)] z-40 max-h-[28rem] overflow-auto rounded-md border border-border bg-popover p-2 text-sm shadow-2xl ring-1 ring-black/30"
			role="listbox"
		>
			{#if loading && !hasResults}
				<div class="px-2 py-3 text-center text-xs text-muted-foreground">{t('search.loading')}</div>
			{:else if !hasResults}
				<div class="px-2 py-3 text-center text-xs text-muted-foreground">{t('search.noResults')}</div>
			{:else}
				{#if results.tags.length > 0}
					<div class="px-2 py-1 text-xs font-semibold text-muted-foreground">
						{t('nav.tags')}
					</div>
					{#each results.tags as tag, i (tag.id)}
						<button
							type="button"
							id="search-opt-{i}"
							role="option"
							aria-selected={activeIndex === i}
							class="flex w-full items-center justify-between gap-2 rounded px-2 py-1.5 text-left hover:bg-accent {activeIndex ===
							i
								? 'bg-accent'
								: ''}"
							onmousedown={(e) => {
								e.preventDefault();
								applyTagFilter(tag.id);
							}}
						>
							<span class="flex items-center gap-2">
								<TagIcon class="size-3.5 text-muted-foreground" />
								<span>{tag.name}</span>
							</span>
							<span class="tabular-nums text-xs text-muted-foreground">{formatNumber(tag.clip_count)}</span>
						</button>
					{/each}
				{/if}

				{#if results.categories.length > 0}
					<div class="mt-1 px-2 py-1 text-xs font-semibold text-muted-foreground">
						{t('nav.games')}
					</div>
					{#each results.categories as c, i (c.id)}
						{@const idx = results.tags.length + i}
						<button
							type="button"
							id="search-opt-{idx}"
							role="option"
							aria-selected={activeIndex === idx}
							class="flex w-full items-center justify-between gap-2 rounded px-2 py-1.5 text-left hover:bg-accent {activeIndex ===
							idx
								? 'bg-accent'
								: ''}"
							onmousedown={(e) => {
								e.preventDefault();
								applyCategory(c.id);
							}}
						>
							<span class="flex items-center gap-1.5">
								<Gamepad2 class="size-3.5 text-muted-foreground" />
								<span>{gameName(c.name)}</span>
							</span>
							<span class="tabular-nums text-xs text-muted-foreground">{formatNumber(c.clip_count ?? 0)}</span>
						</button>
					{/each}
				{/if}

				{#if results.clips.length > 0}
					<div class="mt-1 px-2 py-1 text-xs font-semibold text-muted-foreground">
						{t('nav.clips')}
					</div>
					{#each results.clips as clip, i (clip.id)}
						{@const idx = results.tags.length + results.categories.length + i}
						<button
							type="button"
							id="search-opt-{idx}"
							role="option"
							aria-selected={activeIndex === idx}
							class="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-accent {activeIndex ===
							idx
								? 'bg-accent'
								: ''}"
							onmousedown={(e) => {
								e.preventDefault();
								openClip(clip.id);
							}}
						>
							<Film class="size-3.5 text-muted-foreground" />
							<span class="line-clamp-1 flex-1">{clipTitle(clip)}</span>
							{#if clip.category_name}
								<span class="shrink-0 text-xs text-muted-foreground">{gameName(clip.category_name)}</span>
							{/if}
						</button>
					{/each}
				{/if}
			{/if}
		</div>
	{/if}
</div>
