import { cn } from '$lib/utils';

export const menuContentClass = cn(
	'z-50 min-w-[12rem] overflow-hidden rounded-lg border border-border bg-popover p-1 text-sm shadow-2xl shadow-black/50'
);

export const menuItemClass = cn(
	'relative flex cursor-pointer select-none items-center gap-2 rounded-md px-2 py-1.5 text-sm outline-none transition-colors',
	'hover:bg-accent focus:bg-accent data-[highlighted]:bg-accent data-[disabled]:pointer-events-none data-[disabled]:opacity-50'
);

export const menuLabelClass = cn('px-2 py-1.5 text-xs font-semibold text-muted-foreground');

export const menuSeparatorClass = cn('-mx-1 my-1 h-px bg-border');
