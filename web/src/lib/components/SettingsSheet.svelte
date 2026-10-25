<script lang="ts">
	import Sheet from './ui/Sheet.svelte';
	import InfoTip from './ui/InfoTip.svelte';
	import { ImageDown, LogOut, RefreshCw } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import { getStats, logout, refreshCategoryImages, triggerScan, type Stats } from '$lib/api';
	import { formatBytes } from '$lib/utils';
	import { categories } from '$lib/state/categories.svelte';
	import { session } from '$lib/state/session.svelte';
	import { formatNumber, i18n, LOCALE_NAMES, t, type LangPref } from '$lib/i18n/index.svelte';

	let { open = $bindable(false) }: { open: boolean } = $props();

	let stats = $state<Stats | null>(null);
	let statsError = $state<string | null>(null);
	let scanning = $state(false);
	let fetchingCovers = $state(false);
	let loggingOut = $state(false);

	// Stats walk the preview cache (over a second when cold): loaded only when
	// the panel opens.
	$effect(() => {
		if (!open) return;
		statsError = null;
		getStats()
			.then((s) => (stats = s))
			.catch((e) => (statsError = (e as Error).message));
	});

	async function scan() {
		if (scanning) return;
		scanning = true;
		try {
			const r = await triggerScan();
			toast.success(t('settings.scanDone', { added: r.added, updated: r.updated, deleted: r.deleted }));
			stats = await getStats();
			categories.load(true);
		} catch (e) {
			toast.error(t('settings.scanFailed', { error: (e as Error).message }));
		} finally {
			scanning = false;
		}
	}

	async function fetchCovers() {
		if (fetchingCovers) return;
		fetchingCovers = true;
		try {
			const r = await refreshCategoryImages();
			toast.success(
				t('settings.coversStarted', { updated: r.updated, skipped: r.skipped }) +
					(r.failed ? t('settings.coversFailedPart', { failed: r.failed }) : '')
			);
			// The search goes on server side: reload the list a bit later.
			setTimeout(() => categories.load(true), 15_000);
		} catch (e) {
			toast.error(t('settings.coversError', { error: (e as Error).message }));
		} finally {
			fetchingCovers = false;
		}
	}

	async function signOut() {
		if (loggingOut) return;
		loggingOut = true;
		try {
			await logout();
			open = false;
			await session.load();
			goto('/login', { replaceState: true });
		} catch (e) {
			toast.error(t('settings.logoutFailed', { error: (e as Error).message }));
		} finally {
			loggingOut = false;
		}
	}

	const LANG_OPTIONS: LangPref[] = ['auto', 'en', 'fr'];

	const rows = $derived(
		stats
			? [
					[t('nav.clips'), formatNumber(stats.clips_active)],
					[t('settings.size'), formatBytes(stats.bytes_total)],
					[t('nav.games'), formatNumber(stats.categories_total)],
					[t('nav.tags'), t('settings.tagsValue', { total: stats.tags_total, auto: stats.tags_auto })],
					[
						t('settings.previews'),
						t('settings.previewsReady', { count: stats.preview_done }) +
							(stats.preview_pending ? t('settings.previewsPending', { count: stats.preview_pending }) : '') +
							(stats.preview_failed ? t('settings.previewsFailed', { count: stats.preview_failed }) : '')
					],
					[
						t('settings.cache'),
						t('settings.cacheValue', {
							size: formatBytes(stats.cache_bytes),
							thumbs: stats.cache_thumbs,
							sprites: stats.cache_sprites
						})
					]
				]
			: []
	);
</script>

<Sheet bind:open title={t('nav.settings')}>
	<div class="space-y-8 p-5 text-sm">
		<section>
			<h3 class="mb-3 font-display text-lg font-semibold">{t('nav.library')}</h3>
			{#if statsError}
				<p class="text-destructive">{t('settings.statsError', { error: statsError })}</p>
			{:else if !stats}
				<div class="space-y-3">
					{#each Array(6) as _, i (i)}
						<div class="h-4 animate-pulse rounded bg-muted"></div>
					{/each}
				</div>
			{:else}
				<dl class="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-2.5">
					{#each rows as [k, v] (k)}
						<dt class="text-muted-foreground">{k}</dt>
						<dd class="tabular-nums">{v}</dd>
					{/each}
				</dl>
			{/if}
		</section>

		<section>
			<h3 class="mb-3 font-display text-lg font-semibold">{t('settings.access')}</h3>
			{#if session.data}
				<dl class="grid grid-cols-[7rem_minmax(0,1fr)] items-center gap-x-4 gap-y-2.5">
					<dt class="text-muted-foreground">{t('settings.version')}</dt>
					<dd class="font-mono text-xs">{session.data.version}</dd>
					<dt class="text-muted-foreground">{t('settings.auth')}</dt>
					<dd>{t(`settings.auth.${session.data.auth}`)}</dd>
					{#if session.data.user}
						<dt class="text-muted-foreground">{t('settings.user')}</dt>
						<dd class="truncate">{session.data.user}</dd>
					{/if}
					<dt class="text-muted-foreground">{t('settings.editing')}</dt>
					<dd class="flex items-center gap-1">
						{#if session.canEdit}
							<span class="text-primary">{t('settings.editAllowed')}</span>
						{:else}
							{t('access.readOnly')}
							<InfoTip label={t('access.info')} text={t('access.help')} />
						{/if}
					</dd>
				</dl>
				{#if session.data.auth === 'password' && session.data.authenticated}
					<button
						type="button"
						onclick={signOut}
						disabled={loggingOut}
						class="mt-4 flex w-full items-center gap-3 rounded-lg border border-border px-3 py-2.5 text-left transition-colors hover:border-destructive/50 hover:bg-destructive/10 disabled:opacity-60"
					>
						<LogOut class="size-4 shrink-0 text-muted-foreground" />
						<span class="flex-1 font-medium">{t('settings.logout')}</span>
					</button>
				{/if}
			{/if}
		</section>

		{#if session.canEdit}
			<section class="space-y-3">
				<h3 class="font-display text-lg font-semibold">{t('settings.maintenance')}</h3>
				<div class="space-y-2">
					<button
						type="button"
						onclick={scan}
						disabled={scanning}
						class="flex w-full items-center gap-3 rounded-lg border border-border px-3 py-2.5 text-left transition-colors hover:bg-accent disabled:opacity-60"
					>
						<RefreshCw class="size-4 shrink-0 text-muted-foreground {scanning ? 'animate-spin' : ''}" />
						<span class="flex-1 font-medium">{scanning ? t('settings.scanning') : t('settings.rescan')}</span>
					</button>
					<button
						type="button"
						onclick={fetchCovers}
						disabled={fetchingCovers}
						class="flex w-full items-center gap-3 rounded-lg border border-border px-3 py-2.5 text-left transition-colors hover:bg-accent disabled:opacity-60"
					>
						<ImageDown class="size-4 shrink-0 text-muted-foreground" />
						<span class="flex-1 font-medium">{t('settings.fetchCovers')}</span>
					</button>
				</div>
			</section>
		{/if}

		<section class="space-y-3">
			<h3 class="font-display text-lg font-semibold">{t('settings.language')}</h3>
			<div class="flex h-9 w-fit rounded-md bg-secondary p-0.5" role="group" aria-label={t('settings.language')}>
				{#each LANG_OPTIONS as p (p)}
					<button
						type="button"
						aria-pressed={i18n.pref === p}
						onclick={() => i18n.setPref(p)}
						class="rounded px-3 transition-colors {i18n.pref === p
							? 'bg-accent text-foreground shadow-sm'
							: 'text-muted-foreground hover:text-foreground'}"
					>
						{p === 'auto' ? t('settings.languageAuto') : LOCALE_NAMES[p]}
					</button>
				{/each}
			</div>
		</section>
	</div>
</Sheet>
