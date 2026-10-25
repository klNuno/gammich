<script lang="ts">
	import { DropdownMenu } from 'bits-ui';
	import { ArrowDownUp, Check, Grid2x2, Grid3x3, LayoutGrid, Lock } from '@lucide/svelte';
	import { SORT_OPTIONS, sortLabel as labelOf, type SortOption, type Tag } from '#lib/api.ts';
	import { t, type MessageKey } from '#lib/i18n/index.svelte.ts';
	import { menuContentClass, menuItemClass, menuLabelClass } from '#lib/components/ui/menu.ts';
	import { ui, type Density } from '#lib/state/ui.svelte.ts';
	import { session } from '#lib/state/session.svelte.ts';
	import SearchCommand from './SearchCommand.svelte';
	import InfoTip from './ui/InfoTip.svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';

	type Props = { sort: SortOption; activeTags: Tag[]; query: string };
	let { sort, activeTags, query }: Props = $props();

	function setSort(value: SortOption) {
		const params = new URLSearchParams(page.url.search);
		if (value === 'date_desc') params.delete('sort');
		else params.set('sort', value);
		params.delete('clip');
		const qs = params.toString();
		goto(page.url.pathname + (qs ? `?${qs}` : ''), { replace: true, reset: false });
	}

	const sortLabel = $derived(labelOf(SORT_OPTIONS.includes(sort) ? sort : 'date_desc'));
	// No sort or density on the first-run card.
	const onGrid = $derived(
		(page.url.pathname === '/' || page.url.pathname === '/screenshots') && session.library?.clips !== 0
	);
	const densities: { d: Density; icon: typeof Grid2x2; label: MessageKey }[] = [
		{ d: 'confort', icon: Grid2x2, label: 'density.confort' },
		{ d: 'normal', icon: LayoutGrid, label: 'density.normal' },
		{ d: 'compact', icon: Grid3x3, label: 'density.compact' }
	];
</script>

<header
	class="sticky top-0 z-30 flex h-14 items-center gap-2 border-b border-border bg-background/90 px-3 backdrop-blur-md sm:gap-3 sm:px-5"
>
	<a href="/" class="flex items-center gap-1.5 pr-1 md:hidden" aria-label={t('app.home')}>
		<span class="size-2 rounded-full bg-primary"></span>
		<span class="font-display text-xl font-bold">gammich</span>
	</a>

	<div class="min-w-0 max-w-2xl flex-1">
		<SearchCommand {activeTags} {query} />
	</div>

	<div class="ml-auto flex items-center gap-1">
		{#if onGrid}
			<DropdownMenu.Root>
				<DropdownMenu.Trigger
					class="inline-flex h-10 items-center gap-2 rounded-lg px-2.5 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground data-[state=open]:bg-accent"
					aria-label={t('sort.label', { label: sortLabel })}
				>
					<ArrowDownUp class="size-4" />
					<span class="hidden lg:inline">{sortLabel}</span>
				</DropdownMenu.Trigger>
				<DropdownMenu.Portal>
					<DropdownMenu.Content class={menuContentClass} sideOffset={6} align="end">
						<div class={menuLabelClass}>{t('sort.by')}</div>
						{#each SORT_OPTIONS as opt (opt)}
							<DropdownMenu.Item class={menuItemClass} onSelect={() => setSort(opt)}>
								<Check class="size-4 {opt === sort ? 'text-primary' : 'invisible'}" />
								{labelOf(opt)}
							</DropdownMenu.Item>
						{/each}
					</DropdownMenu.Content>
				</DropdownMenu.Portal>
			</DropdownMenu.Root>

			<div class="hidden items-center rounded-lg bg-muted p-0.5 sm:flex" role="radiogroup" aria-label={t('density.label')}>
				{#each densities as { d, icon: Icon, label } (d)}
					<button
						type="button"
						role="radio"
						aria-checked={ui.density === d}
						aria-label={t(label)}
						title={t(label)}
						onclick={() => ui.setDensity(d)}
						class="grid size-9 place-items-center rounded-md transition-colors {ui.density === d
							? 'bg-accent text-foreground'
							: 'text-muted-foreground hover:text-foreground'}"
					>
						<Icon class="size-4" />
					</button>
				{/each}
			</div>
		{/if}

		{#if !session.canEdit}
			<div
				class="ml-1 flex h-8 items-center gap-1 rounded-full border border-border bg-muted pl-2.5 pr-1 text-xs font-medium text-muted-foreground"
			>
				<Lock class="size-3.5" />
				<span class="hidden sm:inline">{t('access.readOnly')}</span>
				<InfoTip label={t('access.info')} text={t('access.help')} />
			</div>
		{/if}
	</div>
</header>
