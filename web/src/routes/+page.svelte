<script lang="ts">
	import ClipGrid from '$lib/components/ClipGrid.svelte';
	import SetupCard from '$lib/components/SetupCard.svelte';
	import { session } from '$lib/state/session.svelte';
	import { t } from '$lib/i18n/index.svelte';
</script>

<svelte:head>
	<title>gammich</title>
</svelte:head>

<div class="px-4 py-5 md:px-6">
	{#if session.library?.clips === 0}
		<SetupCard />
	{:else}
		<ClipGrid kind="video" emptyLabel={t('library.empty')} />
	{/if}
</div>
