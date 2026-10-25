import { listCategories, patchCategory, type Category } from '#lib/api.ts';

// Game list shared by the sidebar, the game strip, the game header and the
// dialog's game picker: one fetch, refreshed after every change.
class CategoriesState {
	list = $state.raw<Category[]>([]);
	loaded = $state(false);
	error = $state<string | null>(null);
	private inflight: Promise<void> | null = null;

	byId(id: number): Category | undefined {
		return this.list.find((c) => c.id === id);
	}

	// Visible games with at least one clip, most recently played first.
	get recent(): Category[] {
		return this.list
			.filter((c) => !c.hidden && (c.clip_count ?? 0) > 0)
			.toSorted((a, b) => (b.latest_mtime ?? 0) - (a.latest_mtime ?? 0));
	}

	load(force = false): Promise<void> {
		if (this.inflight) return this.inflight;
		if (this.loaded && !force) return Promise.resolve();
		this.inflight = listCategories()
			.then((l) => {
				this.list = l;
				this.loaded = true;
				this.error = null;
			})
			.catch((e) => {
				this.error = (e as Error).message;
			})
			.finally(() => {
				this.inflight = null;
			});
		return this.inflight;
	}

	async update(id: number, patch: { name?: string; hidden?: boolean }): Promise<Category> {
		const updated = await patchCategory(id, patch);
		// The PATCH response has no covers or date: keep the known ones.
		this.list = this.list.map((c) => (c.id === id ? { ...c, ...updated, cover_clip_ids: c.cover_clip_ids, latest_mtime: c.latest_mtime } : c));
		return updated;
	}
}

export const categories = new CategoriesState();
