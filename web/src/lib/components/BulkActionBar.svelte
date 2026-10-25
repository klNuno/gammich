<script lang="ts">
	import { Popover } from 'bits-ui';
	import { Gamepad2, Tag as TagIcon, X } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import GamePicker from './GamePicker.svelte';
	import { bulkCategory, bulkTag, type Category } from '#lib/api.ts';
	import { categories } from '#lib/state/categories.svelte.ts';
	import { gameName } from '#lib/utils.ts';
	import { t } from '#lib/i18n/index.svelte.ts';

	type Props = {
		ids: number[];
		onDone: () => void;
		onCancel: () => void;
	};

	let { ids, onDone, onCancel }: Props = $props();

	let tagName = $state('');
	let busy = $state(false);
	let gameOpen = $state(false);

	const n = $derived(ids.length);
	const clips = $derived(t('count.clips', { count: n }));

	async function applyTag() {
		const name = tagName.trim();
		if (!name || busy) return;
		busy = true;
		try {
			const r = await bulkTag(ids, { tagName: name });
			toast.success(t('bulk.tagAdded', { name, count: r.count }));
			tagName = '';
			window.dispatchEvent(new CustomEvent('gammich:tags-changed'));
			onDone();
		} catch (e) {
			toast.error(t('bulk.tagFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}

	async function applyGame(cat: Category) {
		gameOpen = false;
		if (busy) return;
		busy = true;
		try {
			const r = await bulkCategory(ids, cat.id);
			toast.success(t('bulk.moved', { count: r.count, game: gameName(cat.name) }));
			categories.load(true);
			onDone();
		} catch (e) {
			toast.error(t('common.gameChangeFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}
</script>

<div
	class="fixed inset-x-0 bottom-0 z-40 flex flex-wrap items-center gap-2 border-t border-border bg-popover px-4 pt-3 shadow-2xl shadow-black/60 md:bottom-5 md:left-1/2 md:right-auto md:-translate-x-1/2 md:flex-nowrap md:rounded-xl md:border md:pb-3"
	style:padding-bottom="max(0.75rem, env(safe-area-inset-bottom))"
>
	<span class="mr-1 shrink-0 text-sm font-semibold tabular-nums">{clips}</span>

	<form
		class="flex items-center gap-1"
		onsubmit={(e) => {
			e.preventDefault();
			applyTag();
		}}
	>
		<input
			type="text"
			bind:value={tagName}
			placeholder={t('bulk.tagPlaceholder')}
			aria-label={t('bulk.tagPlaceholder')}
			class="h-9 w-36 rounded-md border border-input bg-background px-2.5 text-sm outline-none focus:border-ring"
		/>
		<button
			type="submit"
			disabled={busy || !tagName.trim()}
			title={t('bulk.addTag')}
			aria-label={t('bulk.addTag')}
			class="grid size-9 place-items-center rounded-md bg-primary text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-40"
		>
			<TagIcon class="size-4" />
		</button>
	</form>

	<Popover.Root bind:open={gameOpen}>
		<Popover.Trigger
			disabled={busy}
			class="inline-flex h-9 items-center gap-2 rounded-md border border-border px-3 text-sm transition-colors hover:bg-accent disabled:opacity-50"
		>
			<Gamepad2 class="size-4 text-muted-foreground" />
			{t('bulk.changeGame')}
		</Popover.Trigger>
		<Popover.Portal>
			<Popover.Content
				side="top"
				sideOffset={8}
				class="z-50 rounded-lg border border-border bg-popover p-1.5 shadow-2xl shadow-black/50"
			>
				<GamePicker onPick={applyGame} />
			</Popover.Content>
		</Popover.Portal>
	</Popover.Root>

	<button
		type="button"
		onclick={onCancel}
		title={t('grid.cancelSelection')}
		aria-label={t('grid.cancelSelection')}
		class="ml-auto grid size-9 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground md:ml-1"
	>
		<X class="size-4" />
	</button>
</div>
