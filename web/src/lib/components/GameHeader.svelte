<script lang="ts">
	import { Check, Eye, EyeOff, Pencil, X } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import GameCover from './GameCover.svelte';
	import { categories } from '#lib/state/categories.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import { formatDateRelative, gameName } from '#lib/utils.ts';
	import type { Category } from '#lib/api.ts';
	import { formatNumber, t } from '#lib/i18n/index.svelte.ts';

	// Header of a game page (?category=): cover, big name, clip count, and the
	// two actions about the game itself.
	type Props = { categoryId: number; fallbackName?: string; total: number; onClose: () => void };
	let { categoryId, fallbackName, total, onClose }: Props = $props();

	const cat = $derived<Category>(
		categories.byId(categoryId) ?? { id: categoryId, slug: '', name: fallbackName ?? '' }
	);
	const name = $derived(gameName(cat.name));
	const hasImage = $derived(!!cat.image_url && cat.image_url !== 'none');

	let renaming = $state(false);
	let draft = $state('');
	let busy = $state(false);

	function startRename() {
		draft = name;
		renaming = true;
	}

	async function saveRename() {
		const next = draft.trim();
		if (!next || next === cat.name || next === name) {
			renaming = false;
			return;
		}
		busy = true;
		try {
			await categories.update(categoryId, { name: next });
			toast.success(t('game.renamed', { name: next }));
			renaming = false;
		} catch (e) {
			toast.error(t('common.renameFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}

	async function toggleHidden() {
		busy = true;
		try {
			const hidden = !cat.hidden;
			await categories.update(categoryId, { hidden });
			toast.success(hidden ? t('game.hiddenToast') : t('game.shownToast'));
		} catch (e) {
			toast.error(t('common.updateFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}
</script>

<header class="relative isolate mb-6 overflow-hidden rounded-xl bg-card">
	<!-- Backdrop: the blurred cover, or the darkened mosaic of the latest clips. -->
	<div class="absolute inset-0 -z-10" aria-hidden="true">
		{#if hasImage}
			<img src={cat.image_url} alt="" class="size-full scale-125 object-cover opacity-45 blur-2xl" />
		{:else}
			<GameCover category={cat} class="size-full opacity-35" />
		{/if}
		<div class="absolute inset-0 bg-gradient-to-r from-background via-background/80 to-background/20"></div>
	</div>

	<div class="flex items-end gap-4 p-4 pt-12 sm:gap-6 sm:p-6">
		<GameCover
			category={cat}
			class="aspect-[2/3] w-20 shrink-0 rounded-md shadow-2xl shadow-black/60 ring-1 ring-white/10 sm:w-28"
		/>

		<div class="min-w-0 flex-1 pb-1">
			{#if renaming}
				<form
					class="flex items-center gap-2"
					onsubmit={(e) => {
						e.preventDefault();
						saveRename();
					}}
				>
					<!-- svelte-ignore a11y_autofocus -->
					<input
						bind:value={draft}
						autofocus
						aria-label={t('game.nameLabel')}
						onkeydown={(e) => e.key === 'Escape' && (renaming = false)}
						class="min-w-0 flex-1 rounded-md border border-input bg-background/80 px-3 py-1.5 font-display text-2xl font-semibold outline-none focus:border-ring sm:text-3xl"
					/>
					<button
						type="submit"
						disabled={busy}
						aria-label={t('game.saveName')}
						class="grid size-10 place-items-center rounded-md bg-primary text-primary-foreground disabled:opacity-50"
					>
						<Check class="size-5" />
					</button>
					<button
						type="button"
						onclick={() => (renaming = false)}
						aria-label={t('common.cancel')}
						class="grid size-10 place-items-center rounded-md hover:bg-accent"
					>
						<X class="size-5" />
					</button>
				</form>
			{:else}
				<h1 class="font-display text-2xl font-bold leading-none tracking-tight text-balance sm:text-5xl">
					{name || t('game.fallbackName')}
				</h1>
			{/if}
			<p class="mt-2 text-sm text-muted-foreground">
				{t('count.clips', { count: total })}{#if cat.latest_mtime}{t('game.latest', {
						when: formatDateRelative(cat.latest_mtime)
					})}{/if}
				{#if cat.hidden}
					<span class="ml-2 rounded bg-secondary px-1.5 py-px text-xs">{t('game.hidden')}</span>
				{/if}
			</p>
		</div>

		{#if !renaming}
			<div class="absolute right-2 top-2 flex items-center gap-1 sm:right-3 sm:top-3">
				{#if session.canEdit}
					<button
						type="button"
						onclick={startRename}
						title={t('game.rename')}
						aria-label={t('game.rename')}
						class="grid size-9 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-white/10 hover:text-foreground"
					>
						<Pencil class="size-4" />
					</button>
					<button
						type="button"
						onclick={toggleHidden}
						disabled={busy}
						title={cat.hidden ? t('game.showTitle') : t('game.hideTitle')}
						aria-label={cat.hidden ? t('game.show') : t('game.hide')}
						class="grid size-9 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-white/10 hover:text-foreground"
					>
						{#if cat.hidden}<Eye class="size-4" />{:else}<EyeOff class="size-4" />{/if}
					</button>
				{/if}
				<button
					type="button"
					onclick={onClose}
					title={t('game.allClips')}
					aria-label={t('game.removeFilter')}
					class="grid size-9 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-white/10 hover:text-foreground"
				>
					<X class="size-4" />
				</button>
			</div>
		{/if}
	</div>
</header>
