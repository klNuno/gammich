<script lang="ts">
	import { BookOpen, FolderOpen, FolderX, RefreshCw, RotateCw } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { triggerScan } from '$lib/api';
	import { categories } from '$lib/state/categories.svelte';
	import { session } from '$lib/state/session.svelte';
	import { t } from '$lib/i18n/index.svelte';

	// First run: the library has no clips. Either the recordings folder is
	// missing (wrong mount or path) or it is empty / not scanned yet.
	const GUIDE_URL = 'https://github.com/klNuno/gammich#quick-start-with-docker';
	const LAYOUT = [
		'├── Counter-Strike 2/',
		'│   ├── 2026-03-14 21-14-05.mp4',
		'│   └── 2026-03-15 20-01-12.mkv',
		'└── Rocket League/',
		'    └── 2026-03-16 22-40-31.mp4'
	].join('\n');

	const lib = $derived(session.library);
	const missing = $derived(lib ? !lib.videos_ok : false);

	let scanning = $state(false);
	let checking = $state(false);

	async function scan() {
		if (scanning) return;
		scanning = true;
		try {
			const r = await triggerScan();
			toast.success(t('settings.scanDone', { added: r.added, updated: r.updated, deleted: r.deleted }));
			await session.load();
			categories.load(true);
		} catch (e) {
			toast.error(t('settings.scanFailed', { error: (e as Error).message }));
		} finally {
			scanning = false;
		}
	}

	async function checkAgain() {
		if (checking) return;
		checking = true;
		await session.load();
		checking = false;
	}
</script>

{#if lib}
	<div class="mx-auto mt-6 max-w-2xl md:mt-12">
		<div
			class="relative overflow-hidden rounded-2xl border-2 bg-card shadow-2xl shadow-black/40 {missing
				? 'border-destructive/40'
				: 'border-primary/40'}"
		>
			<div
				class="pointer-events-none absolute -right-16 -top-20 size-64 rounded-full blur-3xl {missing
					? 'bg-destructive/15'
					: 'bg-primary/15'}"
			></div>

			<div class="relative p-6 sm:p-8">
				<div class="flex items-center gap-4">
					<div
						class="grid size-14 shrink-0 place-items-center rounded-xl {missing
							? 'bg-destructive/15 text-destructive'
							: 'bg-primary/15 text-primary'}"
					>
						{#if missing}<FolderX class="size-7" />{:else}<FolderOpen class="size-7" />{/if}
					</div>
					<h1 class="font-display text-3xl font-bold leading-none tracking-tight sm:text-4xl">
						{missing ? t('setup.missingTitle') : t('setup.emptyTitle')}
					</h1>
				</div>

				<p class="mt-6 text-sm text-muted-foreground">
					{missing ? t('setup.missingBody') : t('setup.emptyBody')}
				</p>
				<code class="mt-1.5 block break-all rounded-md bg-background px-3 py-2 font-mono text-sm text-foreground"
					>{lib.videos_path}</code
				>

				<p class="mt-5 text-sm text-muted-foreground">{t('setup.layout')}</p>
				<pre
					class="mt-1.5 overflow-x-auto rounded-md border border-border bg-background px-4 py-3 font-mono text-[13px] leading-relaxed"><span
						class="text-muted-foreground">{lib.videos_path.replace(/[\\/]+$/, '')}/</span
					>
{LAYOUT}</pre>

				<div class="mt-6 flex flex-wrap items-center gap-2">
					{#if !missing && session.canEdit}
						<button
							type="button"
							onclick={scan}
							disabled={scanning}
							class="inline-flex h-10 items-center gap-2 rounded-lg bg-primary px-4 font-display text-base font-semibold tracking-wide text-primary-foreground transition-[filter,opacity] hover:brightness-110 disabled:opacity-60"
						>
							<RefreshCw class="size-4 {scanning ? 'animate-spin' : ''}" />
							{scanning ? t('settings.scanning') : t('setup.scan')}
						</button>
					{/if}
					<button
						type="button"
						onclick={checkAgain}
						disabled={checking}
						class="inline-flex h-10 items-center gap-2 rounded-lg border border-border px-4 text-sm font-medium transition-colors hover:bg-accent disabled:opacity-60"
					>
						<RotateCw class="size-4 {checking ? 'animate-spin' : ''}" />
						{t('setup.checkAgain')}
					</button>
					<a
						href={GUIDE_URL}
						target="_blank"
						rel="noopener noreferrer"
						class="inline-flex h-10 items-center gap-2 rounded-lg px-3 text-sm font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground sm:ml-auto"
					>
						<BookOpen class="size-4" />
						{t('setup.guide')}
					</a>
				</div>
			</div>
		</div>
	</div>
{/if}
