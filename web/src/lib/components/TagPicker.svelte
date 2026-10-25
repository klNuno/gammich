<script lang="ts">
	import { Plus, X } from '@lucide/svelte';
	import { createTag, listTags, type Tag } from '#lib/api.ts';
	import { toast } from 'svelte-sonner';
	import { tagChipClass } from '#lib/utils.ts';
	import { t } from '#lib/i18n/index.svelte.ts';

	type Props = {
		selected: Tag[];
		onchange: (tags: Tag[]) => void;
		// Tags never offered (the technical tags set by the scan).
		exclude?: (tag: Tag) => boolean;
	};
	let { selected, onchange, exclude }: Props = $props();

	let allTags = $state.raw<Tag[]>([]);
	let input = $state('');
	let busy = $state(false);
	let focused = $state(false);

	function refresh() {
		listTags()
			.then((list) => (allTags = list))
			.catch(() => toast.error(t('tagPicker.unavailable')));
	}

	// A tag created elsewhere (dialog, selection bar): suggestions reloaded.
	$effect(() => {
		refresh();
		window.addEventListener('gammich:tags-changed', refresh);
		return () => window.removeEventListener('gammich:tags-changed', refresh);
	});

	const selectedIds = $derived(new Set(selected.map((tag) => tag.id)));
	const q = $derived(input.trim().toLowerCase());
	const suggestions = $derived(
		allTags
			.filter((tag) => !selectedIds.has(tag.id) && !exclude?.(tag))
			.filter((tag) => (q ? tag.name.toLowerCase().includes(q) : true))
			.slice(0, 8)
	);
	const exact = $derived(suggestions.find((tag) => tag.name.toLowerCase() === q));

	function add(tag: Tag) {
		input = '';
		onchange([...selected, tag]);
	}

	function remove(tag: Tag) {
		onchange(selected.filter((s) => s.id !== tag.id));
	}

	async function createAndAdd() {
		const name = input.trim();
		if (!name || busy) return;
		busy = true;
		try {
			const tag = await createTag(name);
			if (!allTags.some((x) => x.id === tag.id)) allTags = [...allTags, tag];
			add(tag);
		} catch (e) {
			toast.error(t('tagPicker.createFailed', { error: (e as Error).message }));
		} finally {
			busy = false;
		}
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			if (exact) add(exact);
			else if (q) createAndAdd();
		} else if (e.key === 'Backspace' && !input && selected.length > 0) {
			remove(selected[selected.length - 1]);
		}
	}
</script>

<div>
	<div
		class="flex min-h-10 flex-wrap items-center gap-1.5 rounded-md border bg-background px-2 py-1.5 transition-colors {focused
			? 'border-ring'
			: 'border-input'}"
	>
		{#each selected as tag (tag.id)}
			<span class="inline-flex h-6 items-center gap-1 rounded-md pl-2 pr-1 text-sm ring-1 ring-inset {tagChipClass(tag)}">
				{tag.name}
				<button
					type="button"
					class="grid size-4 place-items-center rounded-sm hover:bg-white/10"
					aria-label={t('tagPicker.remove', { name: tag.name })}
					onclick={() => remove(tag)}
				>
					<X class="size-3" />
				</button>
			</span>
		{/each}
		<input
			type="text"
			placeholder={selected.length ? '' : t('tagPicker.add')}
			aria-label={t('tagPicker.add')}
			bind:value={input}
			onkeydown={onKey}
			onfocus={() => (focused = true)}
			onblur={() => (focused = false)}
			class="h-6 min-w-24 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
		/>
	</div>

	{#if suggestions.length > 0 || (q && !exact)}
		<div class="mt-2 flex flex-wrap gap-1.5">
			{#each suggestions as tag (tag.id)}
				<button
					type="button"
					class="h-6 rounded border border-border px-2 text-sm text-muted-foreground transition-colors hover:border-muted-foreground hover:text-foreground"
					onclick={() => add(tag)}
				>
					{tag.name}
				</button>
			{/each}
			{#if q && !exact}
				<button
					type="button"
					class="inline-flex h-6 items-center gap-1 rounded border border-dashed border-primary/50 px-2 text-sm text-primary transition-colors hover:bg-primary/10"
					onclick={createAndAdd}
					disabled={busy}
				>
					<Plus class="size-3.5" /> {t('common.create', { name: input.trim() })}
				</button>
			{/if}
		</div>
	{/if}
</div>
