<script lang="ts">
	import ClipGrid from '$lib/components/ClipGrid.svelte';
	import { t } from '$lib/i18n/index.svelte';
</script>

<svelte:head>
	<title>{t('nav.screenshots')}, gammich</title>
</svelte:head>

<div class="px-4 py-5 md:px-6">
	<h1 class="mb-5 font-display text-4xl font-bold leading-none">{t('nav.screenshots')}</h1>
	<ClipGrid kind="screenshot" emptyLabel={t('screenshots.empty')} />
</div>
