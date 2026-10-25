<script lang="ts">
	import { onMount } from 'svelte';
	import {
		AudioLines,
		Check,
		Gauge,
		Maximize,
		Minimize,
		Pause,
		Play,
		Volume1,
		Volume2,
		VolumeX
	} from '@lucide/svelte';
	import { Popover } from 'bits-ui';
	import { recheckAccess } from '#lib/api.ts';
	import { PcmAudioSync, type PcmStatus } from '#lib/pcmAudio.ts';
	import { t, type MessageKey } from '#lib/i18n/index.svelte.ts';

	// names: one name per track ("" if unknown). guessed: the server picked
	// generic English names ("Game", "Microphone"), translated here.
	type AudioInfo = { clipId: number; tracks: number; pcm: boolean; names?: string[]; guessed?: boolean };

	type Props = {
		src: string;
		poster?: string;
		spriteSrc?: string;
		spriteFrames?: number;
		audio?: AudioInfo;
		class?: string;
		onEnded?: () => void;
	};

	let {
		src,
		poster,
		spriteSrc,
		spriteFrames = 10,
		audio,
		class: className = '',
		onEnded: onEndedCb
	}: Props = $props();

	let videoEl: HTMLVideoElement | undefined = $state();
	let wrapperEl: HTMLDivElement | undefined = $state();
	let scrubEl: HTMLDivElement | undefined = $state();

	let paused = $state(true);
	let muted = $state(false);
	let volume = $state(1);
	let currentTime = $state(0);
	let duration = $state(0);
	let bufferedEnd = $state(0);
	let playbackRate = $state(1);
	let isFullscreen = $state(false);
	let isLoading = $state(false);
	let hasStarted = $state(false);
	let scrubbing = $state(false);
	let lastActivity = $state(Date.now());
	let now = $state(Date.now());
	let hoverPct = $state<number | null>(null);
	let hoverX = $state(0);
	let scrubRect = $state<DOMRect | null>(null);
	let mediaError = $state(false);
	let track = $state(0);
	let pcmStatus = $state<PcmStatus>('idle');
	let trackMenuOpen = $state(false);
	let rateMenuOpen = $state(false);
	let lastPointerType = 'mouse';

	// Web Audio as soon as <video> cannot play the track: PCM audio (Firefox)
	// or any track but the first (no browser can switch tracks in <video>).
	const webAudio = $derived(!!audio && (audio.pcm || track > 0));
	const trackCount = $derived(Math.max(audio?.tracks ?? 0, 1));

	const STORAGE_KEY = 'gammich.player.v2';

	onMount(() => {
		try {
			const p = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}') as { volume?: number; rate?: number };
			if (typeof p.volume === 'number') volume = Math.max(0, Math.min(1, p.volume));
			if (typeof p.rate === 'number') playbackRate = p.rate;
		} catch {
			// storage unavailable: defaults
		}

		const onFsChange = () => (isFullscreen = document.fullscreenElement === wrapperEl);
		document.addEventListener('fullscreenchange', onFsChange);
		const tick = setInterval(() => {
			if (videoEl && !videoEl.paused) now = Date.now();
		}, 500);
		// Keyboard shortcuts active right away.
		wrapperEl?.focus({ preventScroll: true });

		const v = videoEl!;
		v.volume = volume;
		v.playbackRate = playbackRate;
		v.play().catch(() => {
			// Autoplay refused: the "Play" overlay waits for a click.
		});

		return () => {
			document.removeEventListener('fullscreenchange', onFsChange);
			clearInterval(tick);
		};
	});

	function persist() {
		try {
			localStorage.setItem(STORAGE_KEY, JSON.stringify({ volume, rate: playbackRate }));
		} catch {
			// private mode: not persisted
		}
	}

	// Web Audio engine: created when entering webAudio mode, kept while there
	// (a track change only reloads the windows).
	let engine: PcmAudioSync | null = null;
	$effect(() => {
		if (!webAudio || !videoEl || !audio) return;
		const e = new PcmAudioSync(videoEl, audio.clipId, track, (s) => (pcmStatus = s));
		engine = e;
		return () => {
			e.destroy();
			if (engine === e) engine = null;
			pcmStatus = 'idle';
		};
	});
	$effect(() => {
		engine?.setTrack(track);
	});
	// Sound goes either through <video> or through the GainNode, never both.
	$effect(() => {
		if (!videoEl) return;
		videoEl.muted = webAudio || muted;
		videoEl.volume = volume;
		engine?.setVolume(volume, muted);
	});

	const idle = $derived(now - lastActivity > 2200);
	const showControls = $derived(paused || scrubbing || !idle || !hasStarted);
	const progressPct = $derived(duration > 0 ? (currentTime / duration) * 100 : 0);
	const bufferedPct = $derived(duration > 0 ? (bufferedEnd / duration) * 100 : 0);

	function formatTime(s: number): string {
		if (!Number.isFinite(s) || s < 0) return '0:00';
		const total = Math.floor(s);
		const h = Math.floor(total / 3600);
		const m = Math.floor((total % 3600) / 60);
		const sec = total % 60;
		const pad = (n: number) => String(n).padStart(2, '0');
		return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
	}

	function bumpActivity() {
		lastActivity = Date.now();
		now = lastActivity;
	}

	function togglePlay() {
		if (!videoEl) return;
		if (videoEl.paused) videoEl.play().catch(() => {});
		else videoEl.pause();
		if (pcmStatus === 'blocked') engine?.unlock();
		bumpActivity();
	}

	function onVideoClick() {
		// Touch with hidden controls: the first tap reveals them, no surprise pause.
		if (lastPointerType === 'touch' && !showControls) {
			bumpActivity();
			return;
		}
		togglePlay();
	}

	function toggleMute() {
		muted = !muted;
		if (!muted && volume === 0) volume = 0.5;
		persist();
		bumpActivity();
	}

	function setVolume(v: number) {
		volume = Math.max(0, Math.min(1, v));
		if (volume > 0 && muted) muted = false;
		persist();
		bumpActivity();
	}

	function seekTo(t: number) {
		if (!videoEl || !Number.isFinite(duration) || duration === 0) return;
		videoEl.currentTime = Math.max(0, Math.min(duration, t));
		bumpActivity();
	}

	function setRate(r: number) {
		playbackRate = r;
		if (videoEl) videoEl.playbackRate = r;
		persist();
		bumpActivity();
	}

	async function toggleFullscreen() {
		if (!wrapperEl) return;
		try {
			if (document.fullscreenElement) await document.exitFullscreen();
			else await wrapperEl.requestFullscreen();
		} catch {
			// full screen refused
		}
		bumpActivity();
	}

	function onProgress() {
		const b = videoEl?.buffered;
		if (!b || b.length === 0) return;
		try {
			bufferedEnd = b.end(b.length - 1);
		} catch {
			// range removed in the meantime
		}
	}

	function clientXToPct(clientX: number): number {
		if (!scrubRect) return 0;
		return Math.max(0, Math.min(1, (clientX - scrubRect.left) / scrubRect.width));
	}

	function onScrubPointerDown(e: PointerEvent) {
		if (!scrubEl || !videoEl || duration === 0) return;
		e.preventDefault();
		scrubEl.setPointerCapture(e.pointerId);
		scrubRect = scrubEl.getBoundingClientRect();
		scrubbing = true;
		currentTime = clientXToPct(e.clientX) * duration;
		videoEl.currentTime = currentTime;
	}

	function onScrubPointerMove(e: PointerEvent) {
		if (!scrubEl) return;
		// Rect read on every move: a resize or full screen makes it stale.
		const rect = scrubEl.getBoundingClientRect();
		scrubRect = rect;
		hoverX = Math.max(80, Math.min(rect.width - 80, e.clientX - rect.left));
		const pct = clientXToPct(e.clientX);
		hoverPct = pct;
		if (scrubbing && videoEl && duration > 0) {
			currentTime = pct * duration;
			videoEl.currentTime = currentTime;
		}
	}

	function onScrubPointerUp(e: PointerEvent) {
		try {
			scrubEl?.releasePointerCapture(e.pointerId);
		} catch {
			// capture already released
		}
		scrubbing = false;
	}

	function onScrubKey(e: KeyboardEvent) {
		if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
			e.preventDefault();
			e.stopPropagation();
			seekTo(currentTime + (e.key === 'ArrowLeft' ? -5 : 5));
		}
	}

	function onKey(e: KeyboardEvent) {
		const tag = (e.target as HTMLElement)?.tagName;
		if (tag === 'INPUT' || tag === 'TEXTAREA' || e.ctrlKey || e.metaKey || e.altKey) return;
		const k = e.key;
		const handled = (fn: () => void) => {
			e.preventDefault();
			fn();
		};
		if (k === ' ' || k === 'k' || k === 'K') handled(togglePlay);
		else if (k === 'm' || k === 'M') handled(toggleMute);
		else if (k === 'f' || k === 'F') handled(toggleFullscreen);
		else if (k === 'ArrowLeft') handled(() => seekTo(currentTime - 5));
		else if (k === 'ArrowRight') handled(() => seekTo(currentTime + 5));
		else if (k === 'ArrowUp') handled(() => setVolume(volume + 0.05));
		else if (k === 'ArrowDown') handled(() => setVolume(volume - 0.05));
		else if (k === ',' && paused) handled(() => seekTo(currentTime - 1 / 60));
		else if (k === '.' && paused) handled(() => seekTo(currentTime + 1 / 60));
		else if (/^[0-9]$/.test(k)) handled(() => seekTo((duration * parseInt(k, 10)) / 10));
		else if (k === '<') handled(() => setRate(Math.max(0.25, playbackRate - 0.25)));
		else if (k === '>') handled(() => setRate(Math.min(2, playbackRate + 0.25)));
		else if ((k === 'a' || k === 'A') && trackCount > 1) handled(() => (track = (track + 1) % trackCount));
	}

	const RATES = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2];

	const hoverTime = $derived(hoverPct != null ? hoverPct * duration : 0);
	const hoverFrame = $derived(
		spriteSrc && duration > 0 && hoverPct != null
			? Math.min(spriteFrames - 1, Math.max(0, Math.floor(hoverPct * spriteFrames)))
			: 0
	);
	const showTrackMenu = $derived(!!audio && trackCount > 1);
	const GUESSED_NAMES: Record<string, MessageKey> = { Game: 'track.game', Microphone: 'track.mic' };
	function trackLabel(i: number): string {
		const name = audio?.names?.[i];
		if (!name) return t('player.track', { n: i + 1 });
		const key = audio?.guessed ? GUESSED_NAMES[name] : undefined;
		return key ? t(key) : name;
	}
</script>

<svelte:document onmousemove={() => isFullscreen && bumpActivity()} />

<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
	bind:this={wrapperEl}
	role="application"
	tabindex="0"
	aria-label={t('player.label')}
	aria-roledescription={t('player.roleDescription')}
	class="group/player relative overflow-hidden bg-black outline-none {className} {showControls ? '' : 'cursor-none'}"
	onmousemove={bumpActivity}
	onmouseleave={() => {
		if (!paused) lastActivity = 0;
	}}
	onkeydown={onKey}
>
	<!-- svelte-ignore a11y_media_has_caption -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<video
		bind:this={videoEl}
		playsinline
		preload="metadata"
		{poster}
		{src}
		onpointerdown={(e) => (lastPointerType = e.pointerType || 'mouse')}
		onclick={onVideoClick}
		ondblclick={toggleFullscreen}
		onerror={() => {
			mediaError = true;
			recheckAccess();
		}}
		ontimeupdate={() => {
			if (videoEl && !scrubbing) currentTime = videoEl.currentTime;
		}}
		onloadedmetadata={() => {
			if (videoEl) duration = Number.isFinite(videoEl.duration) ? videoEl.duration : 0;
		}}
		ondurationchange={() => {
			if (videoEl) duration = Number.isFinite(videoEl.duration) ? videoEl.duration : 0;
		}}
		onprogress={onProgress}
		onwaiting={() => (isLoading = true)}
		onplaying={() => {
			isLoading = false;
			hasStarted = true;
		}}
		onplay={() => (paused = false)}
		onpause={() => (paused = true)}
		onratechange={() => {
			if (videoEl) playbackRate = videoEl.playbackRate;
		}}
		onended={() => {
			paused = true;
			onEndedCb?.();
		}}
		class="block size-full max-h-[inherit] object-contain"
	></video>

	{#if mediaError}
		<div class="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-black/85 px-6 text-center text-sm">
			<p class="font-medium text-white">{t('player.cannotPlay')}</p>
			<p class="text-white/60">{t('player.downloadHint')}</p>
		</div>
	{/if}

	{#if isLoading && !paused && !mediaError}
		<div class="pointer-events-none absolute inset-0 grid place-items-center">
			<div class="size-11 animate-spin rounded-full border-2 border-white/25 border-t-white"></div>
		</div>
	{/if}

	{#if paused && !mediaError}
		<button
			type="button"
			onclick={togglePlay}
			class="absolute inset-0 grid place-items-center"
			aria-label={t('player.play')}
		>
			<span class="grid size-16 place-items-center rounded-full bg-black/55 text-white backdrop-blur-sm transition-transform hover:scale-105">
				<Play class="size-7 translate-x-0.5 fill-current" />
			</span>
		</button>
	{/if}

	{#if webAudio && !paused && pcmStatus !== 'ready' && pcmStatus !== 'idle'}
		<div class="absolute left-3 top-3 z-10">
			{#if pcmStatus === 'blocked'}
				<button
					type="button"
					onclick={() => engine?.unlock()}
					class="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-semibold text-primary-foreground"
				>
					<Volume2 class="size-4" /> {t('player.enableSound')}
				</button>
			{:else}
				<span class="inline-flex items-center gap-2 rounded-md bg-black/65 px-2.5 py-1 text-xs text-white/85 backdrop-blur-sm">
					<AudioLines class="size-3.5" />
					{pcmStatus === 'error' ? t('player.soundError') : t('player.soundLoading')}
				</span>
			{/if}
		</div>
	{/if}

	<div
		class="pointer-events-none absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/85 via-black/35 to-transparent pb-2 pt-12 transition-opacity duration-200 {showControls
			? 'opacity-100'
			: 'opacity-0'}"
	>
		<div class="pointer-events-auto px-4">
			<div
				bind:this={scrubEl}
				role="slider"
				tabindex="0"
				aria-label={t('player.position')}
				aria-valuemin={0}
				aria-valuemax={Math.round(duration)}
				aria-valuenow={Math.round(currentTime)}
				aria-valuetext={formatTime(currentTime)}
				class="group/scrub relative flex h-4 cursor-pointer touch-none items-center"
				onpointerdown={onScrubPointerDown}
				onpointermove={onScrubPointerMove}
				onpointerup={onScrubPointerUp}
				onpointerenter={(e) => {
					scrubRect = scrubEl!.getBoundingClientRect();
					hoverPct = clientXToPct(e.clientX);
				}}
				onpointerleave={() => {
					if (!scrubbing) hoverPct = null;
				}}
				onkeydown={onScrubKey}
			>
				<div class="relative h-1 w-full rounded-full bg-white/20 transition-[height] group-hover/scrub:h-1.5">
					<div class="absolute inset-y-0 left-0 rounded-full bg-white/30" style:width="{bufferedPct}%"></div>
					<div class="absolute inset-y-0 left-0 rounded-full bg-primary" style:width="{progressPct}%"></div>
				</div>
				<div
					class="absolute top-1/2 size-3.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary shadow transition-transform {scrubbing
						? 'scale-100'
						: 'scale-0 group-hover/scrub:scale-100'}"
					style:left="{progressPct}%"
				></div>

				{#if hoverPct != null && duration > 0}
					<div class="pointer-events-none absolute bottom-6 -translate-x-1/2" style:left="{hoverX}px">
						{#if spriteSrc}
							<div class="w-40 overflow-hidden rounded-md border border-white/15 bg-black shadow-2xl" style="aspect-ratio: 16/9">
								<div
									class="size-full bg-no-repeat"
									style:background-image="url({spriteSrc})"
									style:background-size="{spriteFrames * 100}% 100%"
									style:background-position="{(hoverFrame / (spriteFrames - 1)) * 100}% 0"
								></div>
							</div>
						{/if}
						<div class="mt-1 text-center text-xs font-medium tabular-nums text-white">{formatTime(hoverTime)}</div>
					</div>
				{/if}
			</div>
		</div>

		<div class="pointer-events-auto flex items-center gap-1 px-2 pt-1 text-white sm:gap-2 sm:px-3">
			<button
				type="button"
				onclick={togglePlay}
				aria-label={paused ? t('player.play') : t('player.pause')}
				class="grid size-10 place-items-center rounded-md transition-colors hover:bg-white/15"
			>
				{#if paused}
					<Play class="size-5 translate-x-px fill-current" />
				{:else}
					<Pause class="size-5 fill-current" />
				{/if}
			</button>

			<div class="group/volume flex items-center">
				<button
					type="button"
					onclick={toggleMute}
					aria-label={muted ? t('player.enableSound') : t('player.mute')}
					class="grid size-10 place-items-center rounded-md transition-colors hover:bg-white/15"
				>
					{#if muted || volume === 0}
						<VolumeX class="size-5" />
					{:else if volume < 0.5}
						<Volume1 class="size-5" />
					{:else}
						<Volume2 class="size-5" />
					{/if}
				</button>
				<div class="flex w-0 items-center overflow-hidden transition-all duration-150 group-hover/volume:w-24 focus-within:w-24">
					<input
						type="range"
						min="0"
						max="1"
						step="0.01"
						value={muted ? 0 : volume}
						oninput={(e) => setVolume(parseFloat(e.currentTarget.value))}
						class="mx-2 h-1 w-20 cursor-pointer accent-primary"
						aria-label={t('player.volume')}
					/>
				</div>
			</div>

			<div class="px-1 text-[13px] font-medium tabular-nums text-white/90">
				{formatTime(currentTime)} <span class="text-white/50">/ {formatTime(duration)}</span>
			</div>

			<div class="flex-1"></div>

			{#if showTrackMenu}
				<Popover.Root bind:open={trackMenuOpen}>
					<Popover.Trigger
						aria-label={t('player.audioTrack')}
						class="inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-[13px] font-medium transition-colors hover:bg-white/15"
					>
						<AudioLines class="size-4" />
						<span class="hidden max-w-32 truncate sm:inline">{trackLabel(track)}</span>
					</Popover.Trigger>
					<Popover.Portal to={wrapperEl}>
						<Popover.Content
							side="top"
							align="end"
							sideOffset={8}
							class="z-[60] min-w-48 max-w-64 rounded-lg border border-white/10 bg-black/90 p-1 text-sm text-white shadow-2xl backdrop-blur"
						>
							<div class="px-2 py-1.5 text-xs font-semibold text-white/60">{t('player.audioTrack')}</div>
							{#each Array(trackCount) as _, i (i)}
								<button
									type="button"
									onclick={() => {
										track = i;
										trackMenuOpen = false;
									}}
									class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-white/10"
								>
									<Check class="size-4 shrink-0 {i === track ? 'text-primary' : 'invisible'}" />
									<span class="min-w-0 flex-1 truncate">{trackLabel(i)}</span>
									{#if audio?.names?.[i]}
										<span class="text-xs tabular-nums text-white/40">{i + 1}</span>
									{/if}
								</button>
							{/each}
						</Popover.Content>
					</Popover.Portal>
				</Popover.Root>
			{/if}

			<Popover.Root bind:open={rateMenuOpen}>
				<Popover.Trigger
					aria-label={t('player.speed')}
					class="inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-[13px] font-medium tabular-nums transition-colors hover:bg-white/15"
				>
					<Gauge class="size-4" />
					<span class="hidden sm:inline">{playbackRate}×</span>
				</Popover.Trigger>
				<Popover.Portal to={wrapperEl}>
					<Popover.Content
						side="top"
						align="end"
						sideOffset={8}
						class="z-[60] min-w-32 rounded-lg border border-white/10 bg-black/90 p-1 text-sm text-white shadow-2xl backdrop-blur"
					>
						{#each RATES as r (r)}
							<button
								type="button"
								onclick={() => {
									setRate(r);
									rateMenuOpen = false;
								}}
								class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left tabular-nums hover:bg-white/10"
							>
								<Check class="size-4 {r === playbackRate ? 'text-primary' : 'invisible'}" />
								{r}×
							</button>
						{/each}
					</Popover.Content>
				</Popover.Portal>
			</Popover.Root>

			<button
				type="button"
				onclick={toggleFullscreen}
				aria-label={isFullscreen ? t('player.exitFullscreen') : t('player.fullscreen')}
				class="grid size-10 place-items-center rounded-md transition-colors hover:bg-white/15"
			>
				{#if isFullscreen}
					<Minimize class="size-5" />
				{:else}
					<Maximize class="size-5" />
				{/if}
			</button>
		</div>
	</div>
</div>
