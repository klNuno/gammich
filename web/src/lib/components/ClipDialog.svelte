<script lang="ts">
	import Dialog from './ui/Dialog.svelte';
	import ClipView from './ClipView.svelte';
	import { ChevronLeft, ChevronRight } from '@lucide/svelte';
	import { getClip, type Clip } from '#lib/api.ts';
	import { clipTitle } from '#lib/utils.ts';
	import { t } from '#lib/i18n/index.svelte.ts';

	type Props = {
		clipId: number | null;
		onClose: () => void;
		onClipUpdated?: (clip: Clip) => void;
		siblingIds?: number[];
		onNavigate?: (id: number) => void;
	};

	let { clipId, onClose, onClipUpdated, siblingIds = [], onNavigate }: Props = $props();

	let clip: Clip | null = $state(null);
	let loadError = $state<string | null>(null);
	let abortCtrl: AbortController | null = null;

	const open = $derived(clipId != null);
	const curIndex = $derived(clipId != null ? siblingIds.indexOf(clipId) : -1);
	const prevId = $derived(curIndex > 0 ? siblingIds[curIndex - 1] : null);
	const nextId = $derived(curIndex >= 0 && curIndex < siblingIds.length - 1 ? siblingIds[curIndex + 1] : null);

	$effect(() => {
		abortCtrl?.abort();
		abortCtrl = null;
		if (clipId == null) {
			clip = null;
			loadError = null;
			return;
		}
		load(clipId);
	});

	async function load(id: number) {
		const ctrl = new AbortController();
		abortCtrl = ctrl;
		loadError = null;
		// Keep the previous clip on screen: no black flash between two clips.
		try {
			const got = await getClip(id, { signal: ctrl.signal });
			if (!ctrl.signal.aborted) clip = got;
		} catch (e) {
			if (ctrl.signal.aborted || (e as Error).name === 'AbortError') return;
			loadError = (e as Error).message;
		}
	}

	function navigate(id: number | null) {
		if (id != null) onNavigate?.(id);
	}

	function onWindowKey(e: KeyboardEvent) {
		if (!open) return;
		// The player keeps its arrow keys (seek forward, back) when it has focus.
		const el = document.activeElement as HTMLElement | null;
		if (el?.closest('[role="application"]') || el?.tagName === 'INPUT' || el?.tagName === 'TEXTAREA') return;
		if (e.key === 'ArrowLeft' && prevId != null) {
			e.preventDefault();
			navigate(prevId);
		} else if (e.key === 'ArrowRight' && nextId != null) {
			e.preventDefault();
			navigate(nextId);
		}
	}
</script>

<svelte:window onkeydown={onWindowKey} />

<Dialog
	{open}
	onOpenChange={(v) => !v && onClose()}
	ariaLabel={clip ? clipTitle(clip) : t('clip.fallbackLabel')}
	contentClass="sm:w-[min(98vw,1800px)]"
>
	{#if loadError}
		<div class="flex h-[50vh] flex-col items-center justify-center gap-3 text-sm">
			<p>{t('clip.notFound', { error: loadError })}</p>
			<button
				type="button"
				class="rounded-md border border-border px-3 py-1.5 hover:bg-accent"
				onclick={() => clipId != null && load(clipId)}
			>
				{t('common.retry')}
			</button>
		</div>
	{:else if !clip}
		<div class="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_clamp(300px,24vw,380px)]">
			<div class="grid aspect-video place-items-center bg-black">
				<div class="size-10 animate-spin rounded-full border-2 border-white/20 border-t-white/70"></div>
			</div>
			<div class="hidden flex-col gap-3 p-5 lg:flex">
				<div class="h-4 w-1/3 animate-pulse rounded bg-muted"></div>
				<div class="h-7 w-3/4 animate-pulse rounded bg-muted"></div>
				<div class="h-10 animate-pulse rounded bg-muted/60"></div>
			</div>
		</div>
	{:else}
		{#key clip.id}
			<ClipView
				{clip}
				{onClipUpdated}
				showPageLink
				mediaClass="max-h-[min(88vh,88svh)]"
				onEnded={() => navigate(nextId)}
			>
				{#snippet overlay()}
					{#if prevId != null}
						<button
							type="button"
							onclick={() => navigate(prevId)}
							aria-label={t('clip.prev')}
							class="absolute left-2 top-1/2 z-10 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-black/50 text-white opacity-0 transition-opacity hover:bg-black/75 focus-visible:opacity-100 group-hover/media:opacity-100"
						>
							<ChevronLeft class="size-6" />
						</button>
					{/if}
					{#if nextId != null}
						<button
							type="button"
							onclick={() => navigate(nextId)}
							aria-label={t('clip.next')}
							class="absolute right-2 top-1/2 z-10 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-black/50 text-white opacity-0 transition-opacity hover:bg-black/75 focus-visible:opacity-100 group-hover/media:opacity-100"
						>
							<ChevronRight class="size-6" />
						</button>
					{/if}
				{/snippet}
			</ClipView>
		{/key}
	{/if}
</Dialog>
