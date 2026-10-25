<script lang="ts">
	import { Dialog as Bits } from 'bits-ui';
	import { fade, fly } from 'svelte/transition';
	import { cn } from '$lib/utils';
	import { X } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import { t } from '$lib/i18n/index.svelte';

	type Props = {
		open?: boolean;
		onOpenChange?: (open: boolean) => void;
		side?: 'right' | 'left';
		title?: string;
		description?: string;
		class?: string;
		children?: Snippet;
	};

	let {
		open = $bindable(false),
		onOpenChange,
		side = 'right',
		title,
		description,
		class: className,
		children
	}: Props = $props();
</script>

<Bits.Root bind:open {onOpenChange}>
	<Bits.Portal>
		<Bits.Overlay forceMount>
			{#snippet child({ props, open: isOpen }: { props: Record<string, unknown>; open: boolean })}
				{#if isOpen}
					<div {...props} transition:fade={{ duration: 150 }} class="fixed inset-0 z-50 bg-black/60"></div>
				{/if}
			{/snippet}
		</Bits.Overlay>
		<Bits.Content forceMount>
			{#snippet child({ props, open: isOpen }: { props: Record<string, unknown>; open: boolean })}
				{#if isOpen}
					<div
						{...props}
						transition:fly={{ x: side === 'right' ? 420 : -420, duration: 200, opacity: 1 }}
						class={cn(
							'fixed top-0 z-50 flex h-dvh w-[min(100vw,400px)] flex-col border-border bg-card shadow-2xl',
							side === 'right' ? 'right-0 border-l' : 'left-0 border-r',
							className
						)}
					>
						<div class="flex items-center justify-between gap-4 border-b border-border px-5 py-3">
							<div class="min-w-0 flex-1">
								{#if title}
									<Bits.Title class="truncate font-display text-xl font-semibold">{title}</Bits.Title>
								{/if}
								{#if description}
									<Bits.Description class="text-sm text-muted-foreground">{description}</Bits.Description>
								{/if}
							</div>
							<Bits.Close
								class="grid size-9 place-items-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
								aria-label={t('common.close')}
							>
								<X class="size-4" />
							</Bits.Close>
						</div>
						<div class="flex-1 overflow-y-auto">
							{@render children?.()}
						</div>
					</div>
				{/if}
			{/snippet}
		</Bits.Content>
	</Bits.Portal>
</Bits.Root>
