import { pcmURL, unauthorized } from '$lib/api';

// Audio playback outside <video>: Firefox does not decode the pcm_f32le of OBS
// recordings, and no browser can switch audio tracks in <video>. The video
// plays muted, the server demuxes the chosen track into PCM windows (s16le
// stereo) and this module schedules them in an AudioContext, locked to
// video.currentTime.
//
// The server sends the first byte in ~0.1 s but a whole window in 1 to 4 s
// (ffmpeg reads the interleaved video from storage). So the current window
// starts at the playhead, not at the start of its slot, and each chunk is
// played as soon as it arrives, without waiting for the end.

const WIN = 15; // seconds per window slot
const AHEAD = 6; // preload the next window when less than AHEAD s from the end
const LEAD = 0.06; // scheduling margin, in context seconds
const MAX_DRIFT = 0.15; // audio/video drift allowed before re-anchoring
const MAX_PARALLEL = 2; // the server also caps concurrent demuxes at 2
const RESTART = 3; // restart at the playhead when the stream lags more than RESTART s
// A chunk is 0.25 s at first, then the lead already received over playback
// (4 s at most): sound starts fast without piling up joins between sources.
const FIRST_CHUNK = 0.25;
const MAX_CHUNK = 4;

export type PcmStatus = 'idle' | 'loading' | 'ready' | 'blocked' | 'error';

type Chunk = {
	t: number; // media time of the first sample
	buf: AudioBuffer;
	src?: AudioBufferSourceNode;
};

type Win = {
	from: number; // requested start, between i * WIN and the end of the slot
	reached: number; // media time received so far
	chunks: Chunk[];
	done?: boolean;
	ctrl?: AbortController;
	failed?: number; // timestamp of the last failure
};

type Anchor = { ctx: number; media: number; rate: number };

export class PcmAudioSync {
	private ctx: AudioContext;
	private gain: GainNode;
	private windows = new Map<number, Win>();
	private sources = new Set<AudioBufferSourceNode>();
	private anchor: Anchor | null = null;
	private inflight = 0;
	private volume = 1;
	private muted = false;
	private destroyed = false;
	private listeners: [string, EventListener][] = [];

	constructor(
		private video: HTMLVideoElement,
		private clipId: number,
		private track: number,
		private onStatus: (s: PcmStatus) => void = () => {}
	) {
		this.ctx = new AudioContext({ latencyHint: 'playback' });
		this.gain = this.ctx.createGain();
		this.gain.connect(this.ctx.destination);
		this.on('play', () => this.resume());
		this.on('playing', () => this.reanchor());
		this.on('seeked', () => this.reanchor());
		this.on('ratechange', () => this.reanchor());
		this.on('pause', () => this.stopAll());
		this.on('seeking', () => this.stopAll());
		this.on('waiting', () => this.stopAll());
		this.on('ended', () => this.stopAll());
		this.on('timeupdate', () => this.tick());
		this.focus();
		if (!video.paused) this.resume();
	}

	private on(type: string, fn: () => void) {
		const l = fn as EventListener;
		this.video.addEventListener(type, l);
		this.listeners.push([type, l]);
	}

	setTrack(track: number) {
		if (track === this.track) return;
		this.track = track;
		this.stopAll();
		for (const w of this.windows.values()) w.ctrl?.abort();
		this.windows.clear();
		this.reanchor();
	}

	setVolume(volume: number, muted: boolean) {
		this.volume = volume;
		this.muted = muted;
		this.gain.gain.setTargetAtTime(muted ? 0 : volume, this.ctx.currentTime, 0.015);
	}

	destroy() {
		this.destroyed = true;
		for (const [type, l] of this.listeners) this.video.removeEventListener(type, l);
		this.stopAll();
		for (const w of this.windows.values()) w.ctrl?.abort();
		this.windows.clear();
		this.ctx.close().catch(() => {});
	}

	private indexAt(t: number): number {
		return Math.max(0, Math.floor(t / WIN));
	}

	private async resume() {
		if (this.ctx.state !== 'running') {
			try {
				await this.ctx.resume();
			} catch {
				// ignored: the state is read again just below
			}
		}
		if (this.destroyed) return;
		if (this.ctx.state !== 'running') {
			this.onStatus('blocked');
			return;
		}
		this.gain.gain.value = this.muted ? 0 : this.volume;
		this.reanchor();
	}

	// Called by the UI on a click when the browser blocked audio.
	unlock() {
		this.resume();
	}

	private stopAll() {
		this.anchor = null;
		for (const src of this.sources) {
			src.onended = null;
			try {
				src.stop();
			} catch {
				// source never started
			}
			src.disconnect();
		}
		this.sources.clear();
		for (const w of this.windows.values()) for (const c of w.chunks) c.src = undefined;
	}

	// Re-anchors the audio clock on the video and reschedules loaded chunks.
	// Sound scheduled at context time T comes out at T + outputLatency, so we
	// start on the video instant that will be on screen at that moment.
	private reanchor() {
		this.stopAll();
		const v = this.video;
		if (this.destroyed || v.seeking) return;
		// While paused, still load the position to restart fast.
		const i = this.focus();
		if (v.paused || v.ended || this.ctx.state !== 'running') return;
		const rate = v.playbackRate || 1;
		const latency = this.ctx.outputLatency || this.ctx.baseLatency || 0;
		this.anchor = {
			ctx: this.ctx.currentTime + LEAD,
			media: v.currentTime + (LEAD + latency) * rate,
			rate
		};
		// The anchor, slightly ahead of the video, falls in i or i + 1.
		this.schedule(i);
		this.schedule(i + 1);
		this.report();
	}

	// Media time heard right now, from the current anchor.
	private heardMedia(): number | null {
		if (!this.anchor) return null;
		const latency = this.ctx.outputLatency || this.ctx.baseLatency || 0;
		return this.anchor.media + (this.ctx.currentTime - latency - this.anchor.ctx) * this.anchor.rate;
	}

	private schedule(i: number) {
		const w = this.windows.get(i);
		if (w) for (const c of w.chunks) this.scheduleChunk(c);
	}

	private scheduleChunk(c: Chunk) {
		const a = this.anchor;
		if (!a || c.src) return;
		let when = a.ctx + Math.max(0, c.t - a.media) / a.rate;
		let offset = Math.max(0, a.media - c.t);
		const now = this.ctx.currentTime + 0.02;
		if (when < now) {
			// Chunk arrived late: join the playback in progress.
			offset += (now - when) * a.rate;
			when = now;
		}
		if (offset >= c.buf.duration) return;
		const src = this.ctx.createBufferSource();
		src.buffer = c.buf;
		src.playbackRate.value = a.rate;
		src.connect(this.gain);
		src.start(when, offset);
		src.onended = () => {
			if (c.src === src) c.src = undefined;
			this.sources.delete(src);
			src.disconnect();
		};
		c.src = src;
		this.sources.add(src);
	}

	private tick() {
		const v = this.video;
		// Nothing loads while dragging the bar: seeked takes care of it.
		if (v.seeking) return;
		const i = this.focus();
		if (v.paused || this.ctx.state !== 'running') return;
		const heard = this.heardMedia();
		if (heard == null || Math.abs(v.currentTime - heard) > MAX_DRIFT) {
			this.reanchor();
			return;
		}
		this.schedule(i);
		this.schedule(i + 1);
		this.report();
	}

	// Keeps the playhead window, the previous one if complete and the next one
	// once the current one is complete. The rest is dropped, downloads
	// included: after a jump, the window to play comes before any preload.
	private focus(): number {
		const v = this.video;
		const t = v.currentTime;
		const i = this.indexAt(t);
		let cur = this.windows.get(i);
		// Window starting after the playhead (jump back), or stream lagging too
		// far (jump forward): restart it at the playhead.
		if (cur && (cur.from > t + 0.05 || (!cur.done && !cur.failed && cur.reached < t - RESTART))) {
			cur.ctrl?.abort();
			this.windows.delete(i);
			cur = undefined;
		}
		for (const [k, w] of this.windows) {
			const keep = k === i || (k === i + 1 && (w.done || cur?.done)) || (k === i - 1 && w.done);
			if (!keep) {
				w.ctrl?.abort();
				this.windows.delete(k);
			}
		}
		this.ensure(i, t);
		if (
			this.windows.get(i)?.done &&
			(i + 1) * WIN - t < AHEAD &&
			(i + 1) * WIN < (v.duration || Infinity)
		) {
			this.ensure(i + 1, (i + 1) * WIN);
		}
		return i;
	}

	private report() {
		if (this.destroyed) return;
		const t = this.video.currentTime;
		const w = this.windows.get(this.indexAt(t));
		if (w && w.from <= t + 0.05 && (w.done || w.reached > t)) this.onStatus('ready');
		else if (w?.failed) this.onStatus('error');
		else this.onStatus('loading');
	}

	private ensure(i: number, t: number) {
		if (this.video.duration && i * WIN >= this.video.duration) return;
		const w = this.windows.get(i);
		if (w && !w.failed) return this.pump();
		if (w?.failed && Date.now() - w.failed < 3000) return;
		// Start a little before the playhead, rounded to the millisecond like the server.
		const from = Math.max(i * WIN, Math.floor((t - 0.05) * 1000) / 1000);
		this.windows.set(i, { from, reached: from, chunks: [] });
		this.pump();
	}

	// Starts pending windows, earliest first: focus keeps only i - 1 (already
	// complete), i and i + 1, so i goes first.
	private pump() {
		const waiting = [...this.windows]
			.filter(([, w]) => !w.done && !w.ctrl && !w.failed)
			.sort(([a], [b]) => a - b);
		for (const [i, w] of waiting) {
			if (this.inflight >= MAX_PARALLEL) break;
			this.fetchWindow(i, w);
		}
	}

	private async fetchWindow(i: number, w: Win) {
		const ctrl = new AbortController();
		w.ctrl = ctrl;
		const track = this.track;
		const stale = () => this.destroyed || track !== this.track || this.windows.get(i) !== w;
		this.inflight++;
		this.report();
		try {
			const dur = Math.round(((i + 1) * WIN - w.from) * 1000) / 1000;
			const r = await fetch(pcmURL(this.clipId, track, w.from, dur), { signal: ctrl.signal });
			if (r.status === 401) unauthorized();
			if (!r.ok || !r.body) throw new Error(`pcm ${r.status}`);
			const rate = parseInt(r.headers.get('X-Audio-Rate') ?? '48000', 10) || 48000;
			const channels = parseInt(r.headers.get('X-Audio-Channels') ?? '2', 10) || 2;
			const frameBytes = channels * 2;
			const reader = r.body.getReader();
			let parts: Uint8Array[] = [];
			let size = 0;
			let frames = 0; // already cut into chunks
			for (;;) {
				const { done, value } = await reader.read();
				if (stale()) {
					reader.cancel().catch(() => {});
					return;
				}
				if (value) {
					parts.push(value);
					size += value.byteLength;
				}
				// Pending chunk: [start, start + pending). What lies before the playhead
				// does not count, the target size follows the lead.
				const start = w.from + frames / rate;
				const pending = size / frameBytes / rate;
				const playhead = this.video.currentTime;
				const need = Math.max(FIRST_CHUNK, Math.min(start - playhead, MAX_CHUNK));
				if (start + pending - Math.max(start, playhead) >= need || (done && size >= frameBytes)) {
					const whole = size - (size % frameBytes);
					const bytes = new Uint8Array(size);
					let o = 0;
					for (const p of parts) {
						bytes.set(p, o);
						o += p.byteLength;
					}
					const c: Chunk = { t: start, buf: this.toBuffer(bytes.buffer, whole, rate, channels) };
					frames += whole / frameBytes;
					w.reached = w.from + frames / rate;
					w.chunks.push(c);
					parts = whole < size ? [bytes.slice(whole)] : [];
					size -= whole;
					this.scheduleChunk(c);
					this.report();
				}
				if (done) break;
			}
			w.done = true;
			w.failed = undefined;
		} catch (e) {
			if ((e as Error).name === 'AbortError' || this.destroyed) return;
			w.failed = Date.now();
			console.warn('pcm window', i, e);
		} finally {
			w.ctrl = undefined;
			this.inflight--;
			if (!this.destroyed) {
				if (!this.video.seeking) this.focus();
				this.pump();
				this.report();
			}
		}
	}

	private toBuffer(bytes: ArrayBuffer, length: number, rate: number, channels: number): AudioBuffer {
		const pcm = new Int16Array(bytes, 0, Math.floor(length / 2));
		const frames = Math.floor(pcm.length / channels);
		const buf = this.ctx.createBuffer(channels, Math.max(1, frames), rate);
		for (let c = 0; c < channels; c++) {
			const out = buf.getChannelData(c);
			for (let f = 0, s = c; f < frames; f++, s += channels) out[f] = pcm[s] / 32768;
		}
		return buf;
	}
}
