<script lang="ts">
	import { Popover } from 'bits-ui';
	import {
		Check,
		ChevronDown,
		Clock,
		Download,
		ExternalLink,
		FileVideo,
		HardDrive,
		Monitor,
		Pencil,
		Sparkles,
		X
	} from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import type { Snippet } from 'svelte';
	import GameCover from './GameCover.svelte';
	import GamePicker from './GamePicker.svelte';
	import TagPicker from './TagPicker.svelte';
	import VideoPlayer from './VideoPlayer.svelte';
	import {
		bulkCategory,
		downloadURL,
		patchClip,
		spriteURL,
		streamURL,
		thumbURL,
		type Category,
		type Clip,
		type Tag
	} from '$lib/api';
	import { categories } from '$lib/state/categories.svelte';
	import { session } from '$lib/state/session.svelte';
	import {
		clipTitle,
		formatBytes,
		formatDateTime,
		formatDuration,
		gameName,
		isAutoName,
		isTechTag,
		tagChipClass,
		tagKind
	} from '$lib/utils';
	import { formatNumber, t } from '$lib/i18n/index.svelte';

	// Player + info panel of a clip, shared by the grid dialog and the
	// /clip/[id] page. The parent must recreate it on every clip change
	// ({#key}): the tag autosave fires on unmount.
	type Props = {
		clip: Clip;
		onClipUpdated?: (clip: Clip) => void;
		onEnded?: () => void;
		showPageLink?: boolean;
		mediaClass?: string;
		overlay?: Snippet;
	};

	let {
		clip: initial,
		onClipUpdated,
		onEnded,
		showPageLink = false,
		mediaClass = '',
		overlay
	}: Props = $props();

	// Local copy: changes show without waiting for the parent.
	// svelte-ignore state_referenced_locally
	let clip = $state(initial);
	// svelte-ignore state_referenced_locally
	let userTags = $state<Tag[]>((initial.tags ?? []).filter((tag) => !isTechTag(tag)));
	let renaming = $state(false);
	let draft = $state('');
	let gameOpen = $state(false);
	let savingTags = $state(false);
	let tagsTimer: ReturnType<typeof setTimeout> | null = null;
	let saveSeq = 0;

	const title = $derived(clipTitle(clip));
	const techTags = $derived((clip.tags ?? []).filter(isTechTag));
	const isScreenshot = $derived(clip.kind === 'screenshot');
	const category = $derived<Category | undefined>(
		clip.category_id
			? (categories.byId(clip.category_id) ?? { id: clip.category_id, slug: '', name: clip.category_name ?? '' })
			: undefined
	);

	function emit(next: Clip) {
		clip = next;
		onClipUpdated?.(next);
	}

	// --- Name ---
	function startRename() {
		draft = isAutoName(clip.name) ? '' : clip.name;
		renaming = true;
	}

	async function saveName() {
		const next = draft.trim();
		renaming = false;
		if (!next || next === clip.name) return;
		try {
			emit(await patchClip(clip.id, { name: next }));
		} catch (e) {
			toast.error(t('common.renameFailed', { error: (e as Error).message }));
		}
	}

	// --- Tags: autosave, technical tags are sent back as is ---
	function tagIds() {
		return [...techTags, ...userTags].map((tag) => tag.id);
	}

	async function saveTags() {
		tagsTimer = null;
		const seq = ++saveSeq;
		savingTags = true;
		try {
			const updated = await patchClip(clip.id, { tag_ids: tagIds() });
			if (seq !== saveSeq) return;
			emit(updated);
			window.dispatchEvent(new CustomEvent('gammich:tags-changed'));
		} catch (e) {
			if (seq !== saveSeq) return;
			toast.error(t('clip.tagsNotSaved', { error: (e as Error).message }));
			userTags = (clip.tags ?? []).filter((tag) => !isTechTag(tag));
		} finally {
			if (seq === saveSeq) savingTags = false;
		}
	}

	function onTagsChange(next: Tag[]) {
		userTags = next;
		if (tagsTimer) clearTimeout(tagsTimer);
		tagsTimer = setTimeout(saveTags, 400);
	}

	// A change made right before closing or moving to the next clip is still
	// saved.
	$effect(() => () => {
		if (!tagsTimer) return;
		clearTimeout(tagsTimer);
		const id = clip.id;
		patchClip(id, { tag_ids: tagIds() })
			.then((updated) => {
				onClipUpdated?.(updated);
				window.dispatchEvent(new CustomEvent('gammich:tags-changed'));
			})
			.catch((e) => toast.error(t('clip.tagsNotSaved', { error: (e as Error).message })));
	});

	// --- Game ---
	async function pickGame(c: Category) {
		gameOpen = false;
		if (c.id === clip.category_id) return;
		try {
			await bulkCategory([clip.id], c.id);
			emit({ ...clip, category_id: c.id, category_name: c.name });
			toast.success(t('clip.movedTo', { game: gameName(c.name) }));
			categories.load(true);
		} catch (e) {
			toast.error(t('common.gameChangeFailed', { error: (e as Error).message }));
		}
	}

	// --- Metadata ---
	function audioLabel(c: Clip): string | null {
		if (!c.audio_codec) return c.audio_tracks ? t('count.tracks', { count: c.audio_tracks }) : null;
		const pcm = /^pcm_([suf])(\d+)/.exec(c.audio_codec);
		const codec = pcm
			? t(pcm[1] === 'f' ? 'clip.pcmFloat' : 'clip.pcm', { bits: pcm[2] })
			: ({ aac: 'AAC', opus: 'Opus', mp3: 'MP3', ac3: 'AC-3', flac: 'FLAC' }[c.audio_codec] ??
				c.audio_codec.toUpperCase());
		const parts = [codec];
		if (c.audio_rate) parts.push(`${formatNumber(c.audio_rate / 1000)} kHz`);
		if (c.audio_tracks && c.audio_tracks > 1) parts.push(t('count.tracks', { count: c.audio_tracks }));
		return parts.join(', ');
	}

	const fps = $derived(techTags.find((tag) => tagKind(tag) === 'fps')?.slug.replace('fps', ''));
	const tiles = $derived(
		[
			{ k: t('clip.duration'), icon: Clock, v: !isScreenshot && clip.duration_s ? formatDuration(clip.duration_s) : null },
			{
				k: t('clip.picture'),
				icon: Monitor,
				v: clip.width && clip.height ? `${clip.width}×${clip.height}${fps ? `, ${t('clip.fps', { fps })}` : ''}` : null
			},
			{ k: t('clip.video'), icon: FileVideo, v: !isScreenshot && clip.codec ? clip.codec.toUpperCase() : null },
			{ k: t('clip.size'), icon: HardDrive, v: formatBytes(clip.size_bytes) }
		].filter((tile) => !!tile.v)
	);
	const meta = $derived(
		[
			[t('clip.audio'), !isScreenshot ? audioLabel(clip) : null],
			[t('clip.file'), clip.original_filename]
		].filter((r): r is [string, string] => !!r[1])
	);
	const KIND_ORDER = ['allstar', 'source', 'resolution', 'fps', 'codec'];
	const badges = $derived(
		(clip.tags ?? [])
			.filter((tag) => isTechTag(tag) || tagKind(tag) === 'allstar')
			.toSorted((a, b) => KIND_ORDER.indexOf(tagKind(a)) - KIND_ORDER.indexOf(tagKind(b)))
	);
</script>

<div class="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_clamp(300px,24vw,380px)]">
	<div class="group/media relative flex items-center justify-center bg-black">
		{#if isScreenshot}
			<img src={streamURL(clip.id)} alt={title} class="w-full object-contain {mediaClass}" />
		{:else}
			<VideoPlayer
				src={streamURL(clip.id)}
				poster={thumbURL(clip.id)}
				spriteSrc={clip.preview_status === 'done' ? spriteURL(clip.id) : undefined}
				audio={{
					clipId: clip.id,
					tracks: clip.audio_tracks ?? 1,
					pcm: !!clip.audio_pcm,
					names: clip.audio_track_names,
					guessed: !!clip.audio_track_names_guessed
				}}
				{onEnded}
				class="w-full {mediaClass}"
			/>
		{/if}
		{@render overlay?.()}
	</div>

	<aside
		class="flex min-w-0 flex-col gap-5 border-t-2 border-primary/50 p-5 lg:max-h-[inherit] lg:overflow-y-auto lg:border-l-2 lg:border-t-0"
	>
		<div class="pr-8">
			{#snippet gameLabel()}
				{#if category}
					<GameCover category={category} mosaic={1} class="aspect-[2/3] w-4 shrink-0 rounded-[3px]" />
					<span class="truncate">{gameName(category.name)}</span>
				{:else}
					<span>{t('clip.noGame')}</span>
				{/if}
			{/snippet}
			{#if !session.canEdit}
				<div class="-ml-1 inline-flex max-w-full items-center gap-2 py-0.5 pl-1 text-sm text-muted-foreground">
					{@render gameLabel()}
				</div>
			{:else}
				<Popover.Root bind:open={gameOpen}>
					<Popover.Trigger
						class="-ml-1 inline-flex max-w-full items-center gap-2 rounded-md py-0.5 pl-1 pr-1.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
					>
						{@render gameLabel()}
						<ChevronDown class="size-3.5 shrink-0" />
					</Popover.Trigger>
					<Popover.Portal>
						<Popover.Content
							side="bottom"
							align="start"
							sideOffset={6}
							class="z-[60] rounded-lg border border-border bg-popover p-1.5 shadow-2xl shadow-black/50"
						>
							<GamePicker current={clip.category_id} onPick={pickGame} />
						</Popover.Content>
					</Popover.Portal>
				</Popover.Root>
			{/if}

			{#if renaming}
				<form
					class="mt-1.5 flex items-center gap-1.5"
					onsubmit={(e) => {
						e.preventDefault();
						saveName();
					}}
				>
					<!-- svelte-ignore a11y_autofocus -->
					<input
						bind:value={draft}
						autofocus
						placeholder={title}
						aria-label={t('clip.nameLabel')}
						onkeydown={(e) => {
							if (e.key === 'Escape') {
								// Otherwise bits-ui also closes the dialog.
								e.stopPropagation();
								renaming = false;
							}
						}}
						class="min-w-0 flex-1 rounded-md border border-input bg-background px-2.5 py-1.5 text-base outline-none focus:border-ring"
					/>
					<button
						type="submit"
						aria-label={t('clip.saveName')}
						class="grid size-9 shrink-0 place-items-center rounded-md bg-primary text-primary-foreground"
					>
						<Check class="size-4" />
					</button>
					<button
						type="button"
						aria-label={t('common.cancel')}
						onclick={() => (renaming = false)}
						class="grid size-9 shrink-0 place-items-center rounded-md hover:bg-accent"
					>
						<X class="size-4" />
					</button>
				</form>
			{:else if !session.canEdit}
				<h2 class="mt-1 font-display text-2xl font-semibold leading-tight text-balance">{title}</h2>
			{:else}
				<button type="button" onclick={startRename} class="group/title mt-1 flex w-full items-start gap-2 text-left">
					<h2 class="min-w-0 flex-1 font-display text-2xl font-semibold leading-tight text-balance">{title}</h2>
					<Pencil
						class="mt-1.5 size-4 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover/title:opacity-100"
					/>
					<span class="sr-only">{t('common.rename')}</span>
				</button>
			{/if}
			{#if !isAutoName(clip.name)}
				<p class="mt-1 text-sm text-muted-foreground">{formatDateTime(clip.mtime)}</p>
			{/if}
			{#if badges.length > 0}
				<div class="mt-3 flex flex-wrap gap-1.5">
					{#each badges as tag (tag.id)}
						<span
							class="inline-flex h-6 items-center gap-1 rounded-md px-2 text-xs font-semibold ring-1 ring-inset {tagChipClass(tag)}"
						>
							{#if tagKind(tag) === 'allstar'}<Sparkles class="size-3.5" />{/if}
							{tag.name}
						</span>
					{/each}
				</div>
			{/if}
		</div>

		{#if session.canEdit || userTags.length > 0}
			<section>
				<div class="mb-2 flex items-center justify-between">
					<h3 class="text-sm font-semibold">{t('nav.tags')}</h3>
					{#if savingTags}<span class="text-xs text-muted-foreground">{t('clip.saving')}</span>{/if}
				</div>
				{#if session.canEdit}
					<TagPicker selected={userTags} onchange={onTagsChange} exclude={isTechTag} />
				{:else}
					<div class="flex flex-wrap gap-1.5">
						{#each userTags as tag (tag.id)}
							<span class="inline-flex h-7 items-center rounded-md px-2.5 text-xs font-medium ring-1 ring-inset {tagChipClass(tag)}"
								>{tag.name}</span
							>
						{/each}
					</div>
				{/if}
			</section>
		{/if}

		{#if tiles.length > 0}
			<div class="grid grid-cols-2 gap-2">
				{#each tiles as tile (tile.k)}
					<div class="rounded-lg border border-border bg-background/60 px-3 py-2">
						<div class="flex items-center gap-1.5 text-xs text-muted-foreground">
							<tile.icon class="size-3.5" />
							{tile.k}
						</div>
						<div class="mt-0.5 truncate text-sm font-semibold tabular-nums" title={tile.v}>{tile.v}</div>
					</div>
				{/each}
			</div>
		{/if}

		<dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-5 gap-y-2 text-sm">
			{#each meta as [k, v] (k)}
				<dt class="text-muted-foreground">{k}</dt>
				<dd class="truncate tabular-nums" title={v}>{v}</dd>
			{/each}
		</dl>

		<div class="mt-auto flex gap-2 pt-1">
			<a
				href={downloadURL(clip.id)}
				class="inline-flex h-10 flex-1 items-center justify-center gap-2 rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground transition-colors hover:bg-primary/90"
			>
				<Download class="size-4" /> {t('clip.download')}
			</a>
			{#if showPageLink}
				<a
					href="/clip/{clip.id}"
					title={t('clip.openPage')}
					aria-label={t('clip.openPage')}
					class="grid size-10 place-items-center rounded-md border border-border transition-colors hover:bg-accent"
				>
					<ExternalLink class="size-4" />
				</a>
			{/if}
		</div>
	</aside>
</div>
