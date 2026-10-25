<script lang="ts">
	import { ChevronRight } from '@lucide/svelte';
	import GameCover from './GameCover.svelte';
	import { categories } from '#lib/state/categories.svelte.ts';
	import { gameName } from '#lib/utils.ts';
	import { t } from '#lib/i18n/index.svelte.ts';

	// Strip of recently played games, at the top of the library.
	const games = $derived(categories.recent.slice(0, 16));
</script>

{#if games.length > 0}
	<section class="mb-6" aria-labelledby="strip-title">
		<div class="mb-2 flex items-baseline justify-between">
			<h2 id="strip-title" class="font-display text-lg font-semibold">{t('nav.recent')}</h2>
			<a
				href="/games"
				class="inline-flex items-center gap-0.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
			>
				{t('strip.allGames')} <ChevronRight class="size-4" />
			</a>
		</div>
		<div class="scrollbar-none -mx-4 flex snap-x scroll-px-4 gap-3 overflow-x-auto px-4 pb-1 md:-mx-6 md:scroll-px-6 md:px-6">
			{#each games as g (g.id)}
				<a href="/?category={g.id}" class="group w-24 shrink-0 snap-start sm:w-28" title={gameName(g.name)}>
					<GameCover
						category={g}
						class="aspect-[2/3] w-full rounded-md ring-1 ring-white/5 transition-shadow group-hover:ring-2 group-hover:ring-primary"
					/>
					<div class="mt-1.5 truncate text-xs font-medium">{gameName(g.name)}</div>
					<div class="text-[11px] tabular-nums text-muted-foreground">{t('count.clips', { count: g.clip_count ?? 0 })}</div>
				</a>
			{/each}
		</div>
	</section>
{/if}
