import { en, type MessageKey, type Messages } from './en';
import { fr } from './fr';

export type Locale = 'en' | 'fr';
export type LangPref = 'auto' | Locale;
export type { MessageKey };

type Params = Record<string, string | number>;

const STORAGE_KEY = 'gammich.lang';
const DICTS: Record<Locale, Messages> = { en, fr };
// BCP 47 tag used by every Intl formatter.
const TAGS: Record<Locale, string> = { en: 'en-US', fr: 'fr-FR' };

// Each language shows its own name in the picker.
export const LOCALE_NAMES: Record<Locale, string> = { en: 'English', fr: 'Fran\u00e7ais' };

// First en* or fr* entry of the browser languages wins; anything else is English.
function systemLocale(): Locale {
	if (typeof navigator === 'undefined') return 'en';
	const langs = navigator.languages?.length ? navigator.languages : [navigator.language];
	for (const l of langs) {
		const tag = (l ?? '').toLowerCase();
		if (tag === 'fr' || tag.startsWith('fr-')) return 'fr';
		if (tag === 'en' || tag.startsWith('en-')) return 'en';
	}
	return 'en';
}

function loadPref(): LangPref {
	try {
		const v = localStorage.getItem(STORAGE_KEY);
		return v === 'en' || v === 'fr' ? v : 'auto';
	} catch {
		return 'auto';
	}
}

class I18nState {
	pref = $state<LangPref>(loadPref());
	system = $state<Locale>(systemLocale());
	locale = $derived<Locale>(this.pref === 'auto' ? this.system : this.pref);

	constructor() {
		if (typeof window === 'undefined') return;
		window.addEventListener('languagechange', () => {
			this.system = systemLocale();
			this.syncDocument();
		});
		this.syncDocument();
	}

	get tag(): string {
		return TAGS[this.locale];
	}

	setPref(p: LangPref) {
		this.pref = p;
		try {
			if (p === 'auto') localStorage.removeItem(STORAGE_KEY);
			else localStorage.setItem(STORAGE_KEY, p);
		} catch {
			// localStorage unavailable (private mode): not persisted.
		}
		this.syncDocument();
	}

	private syncDocument() {
		if (typeof document !== 'undefined') document.documentElement.lang = this.locale;
	}
}

export const i18n = new I18nState();

// Formatters are cached per locale and options.
const numberFormats = new Map<string, Intl.NumberFormat>();
const pluralRules = new Map<string, Intl.PluralRules>();

export function formatNumber(n: number, opts?: Intl.NumberFormatOptions): string {
	const key = i18n.tag + JSON.stringify(opts ?? {});
	let f = numberFormats.get(key);
	if (!f) {
		f = new Intl.NumberFormat(i18n.tag, opts);
		numberFormats.set(key, f);
	}
	return f.format(n);
}

function plural(n: number): Intl.LDMLPluralRule {
	let r = pluralRules.get(i18n.tag);
	if (!r) {
		r = new Intl.PluralRules(i18n.tag);
		pluralRules.set(i18n.tag, r);
	}
	return r.select(n);
}

// t('count.clips', { count: 3 }) → "3 clips". Numbers are formatted for the
// active locale. Reads i18n.locale, so templates and deriveds re-run on a switch.
export function t(key: MessageKey, params?: Params): string {
	const msg = DICTS[i18n.locale][key] ?? en[key];
	const text = typeof msg === 'string' ? msg : (msg[plural(Number(params?.count ?? 0))] ?? msg.other);
	if (!params) return text;
	return text.replace(/\{(\w+)\}/g, (m, name: string) => {
		const v = params[name];
		if (v === undefined) return m;
		return typeof v === 'number' ? formatNumber(v) : v;
	});
}
