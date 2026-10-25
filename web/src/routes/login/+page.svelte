<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { ArrowRight, KeyRound, RotateCw } from '@lucide/svelte';
	import { ApiError, login } from '$lib/api';
	import { session } from '$lib/state/session.svelte';
	import { t } from '$lib/i18n/index.svelte';

	let password = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);
	// The password was accepted but /api/session failed: retry only the
	// session, a new login would burn an attempt.
	let sessionFailed = $state(false);
	// Bumped on each failure so the shake animation replays.
	let shake = $state(0);

	// Only same-origin paths, never "//host".
	const next = $derived.by(() => {
		const n = page.url.searchParams.get('next') ?? '/';
		return n.startsWith('/') && !n.startsWith('//') && !n.startsWith('/login') ? n : '/';
	});

	// Already allowed in (signed in, or no password on this server).
	$effect(() => {
		if (session.canRead) goto(next, { replaceState: true });
	});

	async function loadSession() {
		sessionFailed = !(await session.load());
		error = sessionFailed ? t('login.sessionFailed', { error: session.error ?? '' }) : null;
	}

	async function retrySession() {
		busy = true;
		await loadSession();
		busy = false;
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (!password || busy) return;
		busy = true;
		error = null;
		try {
			await login(password);
			password = '';
			await loadSession();
		} catch (err) {
			error = err instanceof ApiError || err instanceof Error ? err.message : String(err);
			shake++;
			password = '';
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head>
	<title>{t('login.title')}, gammich</title>
</svelte:head>

<div class="login-bg relative grid min-h-dvh place-items-center overflow-hidden px-5">
	<form class="relative w-full max-w-sm" onsubmit={submit}>
		<div class="mb-8 flex items-center justify-center gap-3">
			<span class="size-3.5 rounded-full bg-primary shadow-[0_0_18px_var(--color-primary)]"></span>
			<span class="font-display text-6xl font-bold leading-none tracking-tight">gammich</span>
		</div>

		{#key shake}
			<div
				class="rounded-xl border bg-card/90 p-5 shadow-2xl shadow-black/60 backdrop-blur {shake ? 'shake' : ''} {error
					? 'border-destructive/60'
					: 'border-border'}"
			>
				<label
					class="flex h-12 items-center gap-3 rounded-lg border border-input bg-background px-3.5 transition-colors focus-within:border-ring"
				>
					<KeyRound class="size-5 shrink-0 text-muted-foreground" />
					<!-- svelte-ignore a11y_autofocus -->
					<input
						type="password"
						bind:value={password}
						autocomplete="current-password"
						autofocus
						placeholder={t('login.password')}
						aria-label={t('login.password')}
						aria-invalid={!!error}
						class="h-full min-w-0 flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground"
					/>
				</label>

				{#if error}
					<p class="mt-3 text-sm text-destructive" role="alert">{error}</p>
				{/if}

				{#if sessionFailed}
					<button
						type="button"
						onclick={retrySession}
						disabled={busy}
						class="mt-4 inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg border border-border font-display text-lg font-semibold tracking-wide transition-colors hover:bg-accent disabled:opacity-50"
					>
						<RotateCw class="size-5 {busy ? 'animate-spin' : ''}" />
						{t('common.retry')}
					</button>
				{:else}
					<button
						type="submit"
						disabled={busy || !password}
						class="mt-4 inline-flex h-11 w-full items-center justify-center gap-2 rounded-lg bg-primary font-display text-lg font-semibold tracking-wide text-primary-foreground transition-[filter,opacity] hover:brightness-110 disabled:opacity-50"
					>
						{#if busy}
							<span class="size-4 animate-spin rounded-full border-2 border-primary-foreground/30 border-t-primary-foreground"></span>
						{:else}
							{t('login.submit')}
							<ArrowRight class="size-5" />
						{/if}
					</button>
				{/if}
			</div>
		{/key}
	</form>
</div>

<style>
	.login-bg {
		background:
			radial-gradient(60rem 30rem at 50% 0%, color-mix(in oklch, var(--color-primary) 14%, transparent), transparent 70%),
			var(--color-background);
	}
	.shake {
		animation: shake 0.35s ease-in-out;
	}
	@keyframes shake {
		20%,
		60% {
			transform: translateX(-6px);
		}
		40%,
		80% {
			transform: translateX(6px);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.shake {
			animation: none;
		}
	}
</style>
