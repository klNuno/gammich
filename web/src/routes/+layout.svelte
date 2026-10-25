<script lang="ts">
	import '@fontsource/barlow/latin-400.css';
	import '@fontsource/barlow/latin-500.css';
	import '@fontsource/barlow/latin-600.css';
	import '@fontsource/barlow-condensed/latin-600.css';
	import '@fontsource/barlow-condensed/latin-700.css';
	import '../app.css';
	import favicon from '#lib/assets/favicon.svg';
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Toaster } from 'svelte-sonner';
	import AppSidebar from '#lib/components/AppSidebar.svelte';
	import AppTopbar from '#lib/components/AppTopbar.svelte';
	import MobileNav from '#lib/components/MobileNav.svelte';
	import SettingsSheet from '#lib/components/SettingsSheet.svelte';
	import { listTags, listTagsWithCounts, onUnauthorized, type Tag, type SortOption } from '#lib/api.ts';
	import { categories } from '#lib/state/categories.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import { t } from '#lib/i18n/index.svelte.ts';

	let { children } = $props();

	let settingsOpen = $state(false);
	let knownTags: Map<number, Tag> = $state(new Map());

	let activeTagIds = $derived(
		(page.url.searchParams.get('tags') ?? '')
			.split(',')
			.map((s) => parseInt(s, 10))
			.filter((n) => n > 0)
	);
	let activeTags = $derived(activeTagIds.map((id) => knownTags.get(id)).filter((t): t is Tag => !!t));
	let sort = $derived((page.url.searchParams.get('sort') ?? 'date_desc') as SortOption);
	let query = $derived(page.url.searchParams.get('q') ?? '');
	const onLogin = $derived(page.url.pathname === '/login');

	async function refreshTags() {
		try {
			const list = await listTagsWithCounts('', 500);
			const map = new Map<number, Tag>();
			for (const t of list) map.set(t.id, t);
			knownTags = map;
		} catch (e) {
			console.error('tags', e);
		}
	}

	function toLogin() {
		if (page.url.pathname === '/login') return;
		// location, not page.url: shallow routing (?clip=) does not update page.url.
		const next = location.pathname + location.search;
		goto(`/login?next=${encodeURIComponent(next)}`, { replace: true });
	}

	// No read access (password mode, signed out): the login page.
	$effect(() => {
		if (session.data && !session.canRead && !onLogin) untrack(toLogin);
	});

	// Shared lists load once reading is allowed, and again after a new sign-in.
	$effect(() => {
		if (!session.canRead) return;
		untrack(() => {
			categories.load(true);
			refreshTags();
		});
	});

	// An active tag in the URL can be past the 500 cap or created after mount:
	// resolve the missing ones from the full list, once per combination.
	let resolvedMissing = '';
	$effect(() => {
		const missing = activeTagIds.filter((id) => !knownTags.has(id));
		if (missing.length === 0 || knownTags.size === 0) return;
		const key = missing.join(',');
		if (key === resolvedMissing) return;
		resolvedMissing = key;
		listTags()
			.then((list) => {
				const map = new Map(knownTags);
				for (const t of list) if (!map.has(t.id)) map.set(t.id, t);
				knownTags = map;
			})
			.catch(() => {});
	});

	onMount(() => {
		onUnauthorized(() => {
			session.signedOut();
			toLogin();
		});
		session.load();
		const onTagsChanged = () => refreshTags();
		window.addEventListener('gammich:tags-changed', onTagsChanged);
		return () => window.removeEventListener('gammich:tags-changed', onTagsChanged);
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<meta name="theme-color" content="#121419" />
</svelte:head>

<Toaster theme="dark" position="bottom-right" toastOptions={{ class: 'font-sans' }} />

{#if onLogin}
	{@render children()}
{:else if session.error && !session.data}
	<div class="grid min-h-dvh place-items-center px-6">
		<div class="flex flex-col items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 p-6 text-sm">
			<p>{session.error}</p>
			<button class="rounded-md border border-border px-3 py-1.5 hover:bg-accent" onclick={() => session.load()}>
				{t('common.retry')}
			</button>
		</div>
	</div>
{:else if session.canRead}
	<div class="flex min-h-dvh">
		<AppSidebar openSettings={() => (settingsOpen = true)} />
		<div class="flex min-w-0 flex-1 flex-col">
			<AppTopbar {sort} {activeTags} {query} />
			<main class="flex-1 pb-[calc(4.5rem+env(safe-area-inset-bottom))] md:pb-0">
				{@render children()}
			</main>
		</div>
	</div>

	<MobileNav openSettings={() => (settingsOpen = true)} />

	<SettingsSheet bind:open={settingsOpen} />
{/if}
