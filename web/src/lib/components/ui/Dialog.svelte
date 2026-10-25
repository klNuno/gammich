<script lang="ts">
	import { Dialog as Bits } from 'bits-ui';
	import { fade, scale } from 'svelte/transition';
	import { cn } from '$lib/utils';
	import { X } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import { t } from '$lib/i18n/index.svelte';

	type Props = {
		open?: boolean;
		onOpenChange?: (open: boolean) => void;
		title?: string;
		description?: string;
		ariaLabel?: string;
		class?: string;
		contentClass?: string;
		children?: Snippet;
		footer?: Snippet;
		closeButton?: boolean;
	};

	let {
		open = false,
		onOpenChange,
		title,
		description,
		ariaLabel,
		class: className,
		contentClass,
		children,
		footer,
		closeButton = true
	}: Props = $props();

	const hasHeader = $derived(!!title || !!description);
</script>

<Bits.Root {open} {onOpenChange}>
	<Bits.Portal>
		<!-- forceMount + Svelte transitions: bits-ui unmounts the content, so
		     data-[state] classes alone cannot animate the exit. -->
		<Bits.Overlay forceMount>
			{#snippet child({ props, open: isOpen }: { props: Record<string, unknown>; open: boolean })}
				{#if isOpen}
					<div {...props} transition:fade={{ duration: 150 }} class="fixed inset-0 z-50 bg-black/85"></div>
				{/if}
			{/snippet}
		</Bits.Overlay>
		<Bits.Content forceMount>
			{#snippet child({ props, open: isOpen }: { props: Record<string, unknown>; open: boolean })}
				{#if isOpen}
					<!-- Full screen on mobile, centered window from sm up. -->
					<div
						{...props}
						aria-label={title ? undefined : ariaLabel}
						transition:scale={{ duration: 150, start: 0.97 }}
						class={cn(
							'fixed inset-0 z-50 flex flex-col overflow-hidden bg-card text-card-foreground shadow-2xl sm:inset-auto sm:left-1/2 sm:top-1/2 sm:max-h-[min(95vh,95svh)] sm:w-[min(95vw,1200px)] sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-xl sm:border sm:border-border',
							contentClass
						)}
					>
						{#if hasHeader}
							<div class="flex items-start justify-between gap-4 border-b border-border px-5 py-3">
								<div class="min-w-0 flex-1">
									{#if title}
										<Bits.Title class="truncate font-display text-xl font-semibold">{title}</Bits.Title>
									{/if}
									{#if description}
										<Bits.Description class="text-sm text-muted-foreground">{description}</Bits.Description>
									{/if}
								</div>
								{#if closeButton}
									<Bits.Close
										class="grid size-9 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
										aria-label={t('common.close')}
									>
										<X class="size-4" />
									</Bits.Close>
								{/if}
							</div>
						{:else if closeButton}
							<Bits.Close
								class="absolute right-2 top-2 z-20 grid size-9 place-items-center rounded-md bg-black/40 text-white/80 transition-colors hover:bg-black/70 hover:text-white lg:bg-transparent lg:text-muted-foreground lg:hover:bg-accent lg:hover:text-foreground"
								aria-label={t('common.close')}
							>
								<X class="size-5" />
							</Bits.Close>
						{/if}
						<div class={cn('min-h-0 flex-1 overflow-y-auto', className)}>
							{@render children?.()}
						</div>
						{#if footer}
							<div class="border-t border-border px-5 py-3">
								{@render footer()}
							</div>
						{/if}
					</div>
				{/if}
			{/snippet}
		</Bits.Content>
	</Bits.Portal>
</Bits.Root>
