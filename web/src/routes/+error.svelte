<script lang="ts">
	import { page } from '$app/state';
	import { Home, TriangleAlert } from '@lucide/svelte';
	import { t } from '#lib/i18n/index.svelte.ts';
</script>

<svelte:head>
	<title>{page.status}, gammich</title>
</svelte:head>

<div class="flex min-h-[70vh] items-center justify-center bg-background px-6">
	<div class="w-full max-w-md rounded-lg border border-border bg-card p-8 text-center">
		<div class="mx-auto mb-4 grid size-12 place-items-center rounded-full bg-primary/10 text-primary">
			<TriangleAlert class="size-6" />
		</div>
		<p class="font-display text-4xl font-bold tabular-nums">{page.status}</p>
		<p class="mt-2 text-sm text-muted-foreground">
			{page.error?.message ?? t('error.generic')}
		</p>
		<a
			href="/"
			class="mt-6 inline-flex items-center justify-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground transition-colors hover:bg-primary/90"
		>
			<Home class="size-4" />
			{t('error.backToLibrary')}
		</a>
	</div>
</div>
