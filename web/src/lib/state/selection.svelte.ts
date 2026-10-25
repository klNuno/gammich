import { SvelteSet } from 'svelte/reactivity';

// Multi-selection for bulk-tag / bulk-category. Reset on every filter change
// (ClipGrid calls reset()).
class SelectionState {
	active = $state(false);
	ids = new SvelteSet<number>();

	get count() {
		return this.ids.size;
	}

	has(id: number) {
		return this.ids.has(id);
	}

	toggle(id: number) {
		if (this.ids.has(id)) this.ids.delete(id);
		else this.ids.add(id);
	}

	add(id: number) {
		this.ids.add(id);
	}

	enter(id?: number) {
		this.active = true;
		if (id != null) this.ids.add(id);
	}

	// Selects a range between two indexes of the current list (Shift+click).
	selectRange(orderedIds: number[], from: number, to: number) {
		const [lo, hi] = from <= to ? [from, to] : [to, from];
		for (let i = lo; i <= hi; i++) this.ids.add(orderedIds[i]);
	}

	reset() {
		this.active = false;
		this.ids.clear();
	}
}

export const selection = new SelectionState();
