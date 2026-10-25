import { getSession, type Session } from '$lib/api';

// What this browser may do (read, edit) and whether the library is set up.
// Loaded once at startup; the layout waits for it before rendering routes.
class SessionState {
	data = $state.raw<Session | null>(null);
	error = $state<string | null>(null);

	get canRead(): boolean {
		return !!this.data?.can_read;
	}

	get canEdit(): boolean {
		return !!this.data?.can_edit;
	}

	get library(): Session['library'] {
		return this.data?.library;
	}

	// false when the session could not be loaded; the reason is in error.
	async load(): Promise<boolean> {
		try {
			this.data = await getSession();
			this.error = null;
			return true;
		} catch (e) {
			this.error = (e as Error).message;
			return false;
		}
	}

	// A 401 mid-session: the cookie expired or the password changed.
	signedOut() {
		if (!this.data) return;
		this.data = { ...this.data, authenticated: false, can_read: false, can_edit: false, library: undefined };
	}
}

export const session = new SessionState();
