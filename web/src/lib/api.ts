import { t } from '$lib/i18n/index.svelte';

export interface Clip {
	id: number;
	name: string;
	original_filename: string;
	rel_path: string;
	size_bytes: number;
	mtime: number;
	duration_s?: number;
	width?: number;
	height?: number;
	codec?: string;
	category_id?: number;
	category_name?: string;
	kind?: 'video' | 'screenshot';
	preview_status: 'pending' | 'done' | 'failed';
	audio_tracks?: number;
	audio_codec?: string;
	audio_rate?: number;
	audio_channels?: number;
	// pcm_* audio: Firefox does not decode it in <video>, played through Web Audio.
	audio_pcm?: boolean;
	// One name per track ("" if unknown). guessed: names borrowed from other clips
	// of the same recorder, only sent by GET /clips/{id}.
	audio_track_names?: string[];
	audio_track_names_guessed?: boolean;
	tags?: Tag[];
}

export interface Category {
	id: number;
	slug: string;
	name: string;
	hidden?: boolean;
	image_url?: string;
	clip_count?: number;
	latest_mtime?: number;
	cover_clip_ids?: number[];
}

export interface Tag {
	id: number;
	slug: string;
	name: string;
	auto?: boolean;
}

export interface TagWithCount extends Tag {
	clip_count: number;
}

export interface Stats {
	clips_total: number;
	clips_active: number;
	clips_deleted: number;
	bytes_total: number;
	tags_total: number;
	tags_auto: number;
	categories_total: number;
	preview_done: number;
	preview_pending: number;
	preview_failed: number;
	cache_bytes: number;
	cache_sprites: number;
	cache_thumbs: number;
}

export interface ListClipsResponse {
	clips: Clip[];
	total: number;
	page: number;
	per_page: number;
}

export interface ListClipsParams {
	q?: string;
	category?: number;
	sort?: string;
	page?: number;
	per_page?: number;
	tags?: number[];
	kind?: 'video' | 'screenshot' | 'all';
	signal?: AbortSignal;
}

export type SortOption =
	| 'date_desc'
	| 'date_asc'
	| 'name_asc'
	| 'name_desc'
	| 'size_desc'
	| 'size_asc'
	| 'duration_desc'
	| 'duration_asc';

export const SORT_OPTIONS: SortOption[] = [
	'date_desc',
	'date_asc',
	'name_asc',
	'name_desc',
	'size_desc',
	'size_asc',
	'duration_desc',
	'duration_asc'
];

export function sortLabel(value: SortOption): string {
	return t(`sort.${value}`);
}

const base = '';
const FETCH_TIMEOUT_MS = 15_000;

// Combines a 15 s timeout with the caller's abort signal, if any.
function withTimeout(signal?: AbortSignal): AbortSignal {
	const timeout = AbortSignal.timeout(FETCH_TIMEOUT_MS);
	return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

// Readable error when the fetch timed out (TimeoutError) instead of "signal is aborted".
function isTimeout(e: unknown): boolean {
	return e instanceof DOMException && e.name === 'TimeoutError';
}

export class ApiError extends Error {
	constructor(
		message: string,
		public status: number,
		public retryAfter?: number
	) {
		super(message);
	}
}

// Called on any 401 except the login call itself; the layout sends to /login.
let unauthorizedHandler: (() => void) | null = null;
export function onUnauthorized(fn: () => void) {
	unauthorizedHandler = fn;
}

// Also used by requests that bypass get() and send() (PCM windows).
export function unauthorized(): never {
	unauthorizedHandler?.();
	throw new ApiError(t('error.signInRequired'), 401);
}

// Server errors are JSON ({"error": "..."}) or plain text.
async function errorText(r: Response): Promise<string> {
	const text = (await r.text()).trim();
	try {
		const j = JSON.parse(text);
		if (j && typeof j.error === 'string') return j.error;
	} catch {
		// plain text body
	}
	return text || `${r.status} ${r.statusText}`;
}

async function get<T>(path: string, opts?: { signal?: AbortSignal }): Promise<T> {
	try {
		const r = await fetch(base + path, { signal: withTimeout(opts?.signal) });
		if (r.status === 401) unauthorized();
		if (!r.ok) throw new Error(`${r.status} ${r.statusText} on ${path}`);
		return r.json();
	} catch (e) {
		if (isTimeout(e)) throw new Error(t('error.timeout'));
		throw e;
	}
}

async function send<T>(method: string, path: string, body?: unknown): Promise<T> {
	const r = await fetch(path, {
		method,
		headers: body === undefined ? undefined : { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body),
		signal: withTimeout()
	});
	if (r.status === 401) unauthorized();
	if (r.status === 403) throw new Error(t('error.readOnly'));
	if (r.status === 409) throw new Error(t('error.nameTaken'));
	if (!r.ok) throw new Error(await errorText(r));
	return r.status === 204 ? (undefined as T) : r.json();
}

export interface Session {
	version: string;
	auth: 'password' | 'proxy' | 'none';
	authenticated: boolean;
	can_read: boolean;
	can_edit: boolean;
	user?: string;
	library?: { videos_path: string; videos_ok: boolean; clips: number };
}

// Never 401: it is what tells the UI to show the login page.
export async function getSession(): Promise<Session> {
	try {
		const r = await fetch('/api/session', { signal: withTimeout() });
		if (!r.ok) throw new Error(`${r.status} ${r.statusText} on /api/session`);
		return r.json();
	} catch (e) {
		if (isTimeout(e)) throw new Error(t('error.timeout'));
		throw e;
	}
}

// A media element failed: maybe the session expired rather than the codec.
// Sends to the login page when reading is no longer allowed.
export async function recheckAccess(): Promise<void> {
	try {
		if (!(await getSession()).can_read) unauthorizedHandler?.();
	} catch {
		// server unreachable: keep the media error
	}
}

export async function login(password: string): Promise<void> {
	const r = await fetch('/api/login', {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({ password }),
		signal: withTimeout()
	});
	if (r.ok) return;
	if (r.status === 401) throw new ApiError(t('login.wrong'), 401);
	if (r.status === 429) {
		const secs = parseInt(r.headers.get('Retry-After') ?? '', 10);
		const minutes = Math.max(1, Math.ceil((Number.isFinite(secs) ? secs : 600) / 60));
		throw new ApiError(t('login.throttled', { count: minutes }), 429, minutes);
	}
	throw new ApiError(t('login.failed', { error: await errorText(r) }), r.status);
}

export function logout(): Promise<void> {
	return send<void>('POST', '/api/logout');
}

export function listClips(p: ListClipsParams = {}): Promise<ListClipsResponse> {
	const q = new URLSearchParams();
	if (p.q) q.set('q', p.q);
	if (p.category) q.set('category', String(p.category));
	if (p.sort) q.set('sort', p.sort);
	if (p.page) q.set('page', String(p.page));
	if (p.per_page) q.set('per_page', String(p.per_page));
	if (p.tags && p.tags.length > 0) q.set('tags', p.tags.join(','));
	if (p.kind) q.set('kind', p.kind);
	return get<ListClipsResponse>('/api/clips?' + q.toString(), { signal: p.signal });
}

export function getClip(id: number, opts?: { signal?: AbortSignal }): Promise<Clip> {
	return get<Clip>(`/api/clips/${id}`, opts);
}

export function listCategories(): Promise<Category[]> {
	return get<Category[]>('/api/categories');
}

export function listTags(): Promise<Tag[]> {
	return get<Tag[]>('/api/tags');
}

export function listTagsWithCounts(query = '', limit = 200): Promise<TagWithCount[]> {
	const q = new URLSearchParams({ with_counts: '1', limit: String(limit) });
	if (query) q.set('q', query);
	return get<TagWithCount[]>('/api/tags?' + q.toString());
}

export function getStats(): Promise<Stats> {
	return get<Stats>('/api/stats');
}

export function triggerScan(): Promise<{ added: number; updated: number; deleted: number; errors: number }> {
	return send('POST', '/api/scan');
}

export function createTag(name: string): Promise<Tag> {
	return send<Tag>('POST', '/api/tags', { name });
}

export async function bulkTag(
	clipIds: number[],
	opts: { tagId?: number; tagName?: string }
): Promise<{ count: number }> {
	const body: Record<string, unknown> = { clip_ids: clipIds };
	if (opts.tagId) body.tag_id = opts.tagId;
	else if (opts.tagName) body.tag_names = [opts.tagName];
	const data = await send<{ count: number }>('POST', '/api/clips/bulk-tag', body);
	return { count: data.count };
}

export async function bulkCategory(clipIds: number[], categoryId: number): Promise<{ count: number }> {
	const data = await send<{ count: number }>('POST', '/api/clips/bulk-category', {
		clip_ids: clipIds,
		category_id: categoryId
	});
	return { count: data.count };
}

export interface ClipPatch {
	name?: string;
	tag_ids?: number[];
	add_tag_names?: string[];
	remove_tag_ids?: number[];
	category_id?: number;
}

export interface SearchHit<T> {
	type: 'tag' | 'category' | 'clip';
	item: T;
}

export interface SearchResponse {
	tags: TagWithCount[];
	categories: Category[];
	clips: Clip[];
}

export function unifiedSearch(query: string, limit = 6): Promise<SearchResponse> {
	const q = new URLSearchParams({ q: query, limit: String(limit) });
	return get<SearchResponse>('/api/search?' + q.toString());
}

export function refreshCategoryImages(): Promise<{ updated: number; skipped: number; failed: number }> {
	return send('POST', '/api/categories/refresh-images');
}

export function createCategory(name: string): Promise<Category> {
	return send<Category>('POST', '/api/categories', { name });
}

export function clearCategoryImages(): Promise<{ cleared: number }> {
	return send('POST', '/api/categories/clear-images');
}


export function patchCategory(id: number, patch: { name?: string; hidden?: boolean }): Promise<Category> {
	return send<Category>('PATCH', `/api/categories/${id}`, patch);
}

export function renameTag(id: number, name: string): Promise<Tag> {
	return send<Tag>('PATCH', `/api/tags/${id}`, { name });
}

export function deleteTag(id: number): Promise<void> {
	return send<void>('DELETE', `/api/tags/${id}`);
}

export function patchClip(id: number, patch: ClipPatch): Promise<Clip> {
	return send<Clip>('PATCH', `/api/clips/${id}`, patch);
}

export function streamURL(id: number): string {
	return `/api/clips/${id}/stream`;
}

export function downloadURL(id: number): string {
	return `/api/clips/${id}/download`;
}

export function thumbURL(id: number): string {
	return `/api/clips/${id}/thumb.jpg`;
}

export function spriteURL(id: number): string {
	return `/api/clips/${id}/sprite.jpg`;
}

export function pcmURL(id: number, track: number, from: number, dur: number): string {
	return `/api/clips/${id}/pcm?track=${track}&from=${from}&dur=${dur}`;
}
