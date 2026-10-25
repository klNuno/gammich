<script lang="ts">
	import { page } from '$app/state';
	import { ArrowLeft } from '@lucide/svelte';
	import ClipView from '$lib/components/ClipView.svelte';
	import { getClip, type Clip } from '$lib/api';
	import { clipTitle } from '$lib/utils';
	import { t } from '$lib/i18n/index.svelte';

	let clip = $state<Clip | null>(null);
	let error = $state<string | null>(null);

	// Reloads on every id change, aborts the previous fetch.
	$effect(() => {
		const id = parseInt(page.params.id ?? '', 10);
		if (!Number.isFinite(id) || id <= 0) {
			error = t('clip.badLink');
			return;
		}
		const controller = new AbortController();
		error = null;
		clip = null;
		getClip(id, { signal: controller.signal })
			.then((c) => {
				if (!controller.signal.aborted) clip = c;
			})
			.catch((e) => {
				if (!controller.signal.aborted) error = (e as Error).message;
			});
		return () => controller.abort();
	});

	const back = $derived(clip?.category_id ? `/?category=${clip.category_id}` : '/');
</script>

<svelte:head>
	<title>{clip ? `${clipTitle(clip)}, gammich` : 'gammich'}</title>
</svelte:head>

<div class="mx-auto max-w-[1600px] px-4 py-4 md:px-6">
	<a
		href={back}
		class="mb-3 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
	>
		<ArrowLeft class="size-4" />
		{clip?.category_name ? t('clip.backToGame') : t('nav.library')}
	</a>

	{#if error}
		<p class="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm">{t('clip.notFound', { error })}</p>
	{:else if !clip}
		<div class="aspect-video animate-pulse rounded-xl bg-muted"></div>
	{:else}
		<div class="overflow-hidden rounded-xl border border-border bg-card">
			{#key clip.id}
				<ClipView {clip} onClipUpdated={(c) => (clip = c)} mediaClass="max-h-[80vh]" />
			{/key}
		</div>
	{/if}
</div>
