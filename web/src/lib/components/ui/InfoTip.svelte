<script lang="ts">
	import { Popover } from 'bits-ui';
	import { Info } from '@lucide/svelte';

	// Small "i" button that opens a short explanation on click or tap.
	let { label, text, class: klass = '' }: { label: string; text: string; class?: string } = $props();
</script>

<Popover.Root>
	<Popover.Trigger
		aria-label={label}
		class="inline-grid size-6 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-foreground {klass}"
	>
		<Info class="size-4" />
	</Popover.Trigger>
	<Popover.Portal>
		<Popover.Content
			sideOffset={6}
			collisionPadding={12}
			class="z-[60] max-w-72 rounded-lg border border-border bg-popover px-3.5 py-3 text-sm leading-relaxed text-popover-foreground shadow-2xl shadow-black/50"
		>
			{text}
		</Popover.Content>
	</Popover.Portal>
</Popover.Root>
