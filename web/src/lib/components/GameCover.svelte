<script lang="ts">
	import { thumbURL, type Category } from '#lib/api.ts';
	import { cn, gameName } from '#lib/utils.ts';

	// Game cover: the Steam image if found, else a mosaic of the latest clips
	// (an "OBS" or "Desktop" folder has no cover), else initials on a flat
	// color derived from the name.
	type Props = { category: Category; class?: string; mosaic?: number };
	let { category, class: className = '', mosaic = 4 }: Props = $props();

	let imageFailed = $state(false);
	$effect(() => {
		category.image_url;
		imageFailed = false;
	});

	const covers = $derived((category.cover_clip_ids ?? []).slice(0, mosaic));
	const name = $derived(gameName(category.name));
	const initials = $derived(
		name
			.split(/[\s:-]+/)
			.filter((w) => /^[\p{L}\p{N}]/u.test(w))
			.slice(0, 2)
			.map((w) => w[0].toUpperCase())
			.join('')
	);
	const hue = $derived.by(() => {
		let h = 0;
		for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
		return h % 360;
	});
</script>

<div class={cn('@container relative overflow-hidden bg-muted', className)}>
	{#if category.image_url && category.image_url !== 'none' && !imageFailed}
		<img
			src={category.image_url}
			alt=""
			loading="lazy"
			onerror={() => (imageFailed = true)}
			class="absolute inset-0 size-full object-cover"
		/>
	{:else if covers.length >= 2}
		<div class="absolute inset-0 grid {covers.length >= 4 ? 'grid-cols-2 grid-rows-2' : 'grid-rows-2'} gap-px bg-background">
			{#each covers.slice(0, covers.length >= 4 ? 4 : 2) as id (id)}
				<img src={thumbURL(id)} alt="" loading="lazy" class="size-full object-cover" />
			{/each}
		</div>
	{:else if covers.length === 1}
		<img src={thumbURL(covers[0])} alt="" loading="lazy" class="absolute inset-0 size-full object-cover" />
	{:else}
		<div
			class="absolute inset-0 grid place-items-center font-display text-[36cqw] font-semibold leading-none text-white/70"
			style="background: linear-gradient(150deg, oklch(0.36 0.07 {hue}), oklch(0.22 0.04 {(hue + 40) % 360}))"
		>
			{initials}
		</div>
	{/if}
</div>
