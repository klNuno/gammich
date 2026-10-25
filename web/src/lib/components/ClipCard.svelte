<script lang="ts">
	import { Check, Image as ImageIcon, Sparkles } from '@lucide/svelte';
	import { spriteURL, streamURL, thumbURL, type Clip } from '$lib/api';
	import {
		clipTitle,
		formatDateRelative,
		formatDuration,
		gameName,
		isAutoName,
		isTechTag,
		tagChipClass,
		tagKind
	} from '$lib/utils';
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { selection } from '$lib/state/selection.svelte';
	import { session } from '$lib/state/session.svelte';
	import { t } from '$lib/i18n/index.svelte';

	type Props = {
		clip: Clip;
		index?: number;
		showGame?: boolean;
		onSelect?: (e: MouseEvent | KeyboardEvent, index: number) => void;
	};

	let { clip, index = 0, showGame = true, onSelect }: Props = $props();

	let hovering = $state(false);
	let frame = $state(0);
	let screenshotThumbFailed = $state(false);
	let thumbFailed = $state(false);
	const FRAMES = 10;

	// Hover (and the sprite preload) only makes sense with a mouse: on touch,
	// mouseenter is simulated on tap.
	const canHover = typeof window !== 'undefined' && window.matchMedia('(hover: hover)').matches;

	let longPressTimer: ReturnType<typeof setTimeout> | null = null;

	$effect(() => {
		clip.id;
		screenshotThumbFailed = false;
		thumbFailed = false;
	});

	$effect(() => () => {
		if (longPressTimer) clearTimeout(longPressTimer);
	});

	function onMove(e: MouseEvent) {
		if (!canHover || isScreenshot || !ready) return;
		hovering = true;
		const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
		const pct = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
		frame = Math.min(FRAMES - 1, Math.floor(pct * FRAMES));
	}

	function onLeave() {
		hovering = false;
		frame = 0;
		cancelLongPress();
	}

	function open() {
		const params = new URLSearchParams(page.url.searchParams);
		params.set('clip', String(clip.id));
		pushState(`?${params.toString()}`, { clipId: clip.id });
	}

	function onClick(e: MouseEvent) {
		if (selection.active) {
			e.preventDefault();
			onSelect?.(e, index);
			return;
		}
		// Modified or middle click: native navigation to /clip/[id].
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
		e.preventDefault();
		open();
	}

	// Touch long press: enters selection mode.
	function onPointerDown(e: PointerEvent) {
		if (e.pointerType !== 'touch' || selection.active || !session.canEdit) return;
		longPressTimer = setTimeout(() => selection.enter(clip.id), 500);
	}
	function cancelLongPress() {
		if (longPressTimer) {
			clearTimeout(longPressTimer);
			longPressTimer = null;
		}
	}

	function toggleCheckbox(e: MouseEvent) {
		e.preventDefault();
		e.stopPropagation();
		if (!selection.active) selection.enter();
		onSelect?.(e, index);
	}

	const isScreenshot = $derived(clip.kind === 'screenshot');
	const ready = $derived(isScreenshot || clip.preview_status === 'done');
	const title = $derived(clipTitle(clip, true));
	// A generated title already carries the date: no duplicate below.
	const autoName = $derived(isAutoName(clip.name));
	const userTags = $derived((clip.tags ?? []).filter((t) => !isTechTag(t)));
	// Technical chips in a fixed order: source, resolution, fps, codec.
	const KIND_ORDER = ['source', 'resolution', 'fps', 'codec'];
	const techTags = $derived(
		(clip.tags ?? [])
			.filter(isTechTag)
			.toSorted((a, b) => KIND_ORDER.indexOf(tagKind(a)) - KIND_ORDER.indexOf(tagKind(b)))
	);
	const selected = $derived(selection.has(clip.id));
	const screenshotSrc = $derived(screenshotThumbFailed ? streamURL(clip.id) : thumbURL(clip.id));
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="group relative rounded-xl bg-card ring-1 transition-shadow {selected
		? 'ring-2 ring-primary'
		: 'ring-border hover:ring-primary/45'}"
	onmousemove={onMove}
	onmouseleave={onLeave}
	onpointerdown={onPointerDown}
	onpointerup={cancelLongPress}
	onpointercancel={cancelLongPress}
>
	<a
		href="/clip/{clip.id}"
		aria-label={title}
		class="absolute inset-0 z-[1] rounded-xl focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
		onclick={onClick}
	></a>

	<div class="relative aspect-video overflow-hidden rounded-t-xl bg-black">
		{#if isScreenshot}
			<img
				src={screenshotSrc}
				alt=""
				loading="lazy"
				onerror={() => (screenshotThumbFailed = true)}
				class="absolute inset-0 size-full object-cover"
			/>
		{:else if ready && !thumbFailed}
			<img
				src={thumbURL(clip.id)}
				alt=""
				loading="lazy"
				onerror={() => (thumbFailed = true)}
				class="absolute inset-0 size-full object-cover"
			/>
			{#if hovering}
				<div
					class="absolute inset-0 bg-no-repeat"
					style:background-image="url({spriteURL(clip.id)})"
					style:background-size="{FRAMES * 100}% 100%"
					style:background-position="{(frame / (FRAMES - 1)) * 100}% 0"
				></div>
				<div class="absolute inset-x-0 bottom-0 h-0.5 bg-white/15">
					<div class="h-full bg-primary" style:width="{((frame + 1) / FRAMES) * 100}%"></div>
				</div>
			{/if}
		{:else}
			<div class="absolute inset-0 grid place-items-center bg-muted text-xs text-muted-foreground">
				{thumbFailed || clip.preview_status === 'failed' ? t('card.previewUnavailable') : t('card.previewPending')}
			</div>
		{/if}

		{#if isScreenshot}
			<span class="absolute bottom-1.5 right-1.5 grid size-6 place-items-center rounded bg-black/70 text-white">
				<ImageIcon class="size-3.5" />
			</span>
		{:else if clip.duration_s}
			<span
				class="absolute bottom-1.5 right-1.5 rounded bg-black/75 px-1.5 py-px text-xs font-semibold tabular-nums text-white"
			>
				{formatDuration(clip.duration_s)}
			</span>
		{/if}

		{#if session.canEdit}
			<button
				type="button"
				onclick={toggleCheckbox}
				aria-label={selected ? t('card.deselect') : t('card.select')}
				aria-pressed={selected}
				class="absolute left-1.5 top-1.5 z-[2] grid size-6 place-items-center rounded-md border transition-opacity {selected
					? 'border-primary bg-primary text-primary-foreground'
					: 'border-white/70 bg-black/40 text-transparent hover:border-white'} {selection.active
					? 'opacity-100'
					: 'opacity-0 group-hover:opacity-100 focus-visible:opacity-100'}"
			>
				<Check class="size-4" strokeWidth={3} />
			</button>
		{/if}
	</div>

	<div class="px-3 pb-3 pt-2.5">
		<h3 class="truncate text-sm font-semibold leading-snug" title={clip.name}>{title}</h3>
		{#if (showGame && clip.category_name) || !autoName}
			<div class="mt-0.5 flex items-center gap-2 text-xs text-muted-foreground">
				{#if showGame && clip.category_name}
					<span class="min-w-0 truncate">{gameName(clip.category_name)}</span>
				{/if}
				{#if !autoName}
					<span class="ml-auto shrink-0 tabular-nums">{formatDateRelative(clip.mtime)}</span>
				{/if}
			</div>
		{/if}
		{#if techTags.length > 0 || userTags.length > 0}
			<div class="mt-2 flex flex-wrap gap-1">
				{#each [...techTags, ...userTags.slice(0, 2)] as tag (tag.id)}
					<span
						class="inline-flex h-5 items-center gap-1 rounded-md px-1.5 text-[11px] font-semibold ring-1 ring-inset {tagChipClass(tag)}"
					>
						{#if tagKind(tag) === 'allstar'}<Sparkles class="size-3" />{/if}
						{tag.name}
					</span>
				{/each}
				{#if userTags.length > 2}
					<span class="inline-flex h-5 items-center px-1 text-[11px] text-muted-foreground">+{userTags.length - 2}</span>
				{/if}
			</div>
		{/if}
	</div>
</div>
