export type Density = 'confort' | 'normal' | 'compact';

// Column classes per density. Static strings, so Tailwind scans them at build time.
export const DENSITY_GRID: Record<Density, string> = {
	confort: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4',
	normal: 'grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 2xl:grid-cols-6',
	compact: 'grid-cols-3 sm:grid-cols-4 lg:grid-cols-6 xl:grid-cols-8 2xl:grid-cols-10'
};

const KEY = 'gammich.density';

function load(): Density {
	if (typeof localStorage === 'undefined') return 'normal';
	const v = localStorage.getItem(KEY);
	return v === 'confort' || v === 'compact' || v === 'normal' ? v : 'normal';
}

class UIState {
	density = $state<Density>(load());

	setDensity(d: Density) {
		this.density = d;
		try {
			localStorage.setItem(KEY, d);
		} catch {
			// localStorage unavailable (private mode): not persisted.
		}
	}
}

export const ui = new UIState();
