#!/usr/bin/env bash
# Builds a small demo library in _fixtures/ (or $1) with ffmpeg: a few games,
# each with clips named like ShadowPlay or OBS recordings, multi-track audio,
# PCM audio (the Firefox Web Audio path) and a screenshot. Used by `make dev`.
set -euo pipefail

out=${1:-_fixtures}
ff=${FFMPEG:-ffmpeg}
command -v "$ff" >/dev/null || { echo "ffmpeg not found (set FFMPEG)" >&2; exit 1; }

clip() { # clip <path> <lavfi video> <seconds> <extra args...>
	local path=$1 src=$2 dur=$3
	shift 3
	mkdir -p "$(dirname "$path")"
	[[ -f $path ]] && return
	"$ff" -v error -y -f lavfi -i "$src,format=yuv420p" \
		-f lavfi -i "sine=frequency=330:sample_rate=48000:duration=$dur" \
		-t "$dur" "$@" "$path"
	echo "  $path"
}

h264=(-map 0:v -map 1:a -c:v libx264 -preset veryfast -crf 28 -c:a aac -b:a 96k)

echo "==> $out"
cs="$out/Counter-Strike 2"
clip "$cs/Counter-Strike 2 2026.03.14 - 21.14.05.03.DVR.mp4" "mandelbrot=size=1280x720:rate=30" 12 "${h264[@]}"
clip "$cs/Counter-Strike 2 2026.03.18 - 22.40.11.07.DVR.mp4" "testsrc2=size=1280x720:rate=30" 8 "${h264[@]}"
clip "$cs/Counter-Strike 2 2026.04.02 - 19.05.43.01.DVR.mp4" "cellauto=size=1280x720:rate=30:rule=110" 10 "${h264[@]}"

rl="$out/Rocket League"
# OBS MKV with two named audio tracks.
for t in "2026-03-15 20-01-12" "2026-03-21 18-30-00"; do
	p="$rl/$t.mkv"
	mkdir -p "$rl"
	[[ -f $p ]] || {
		"$ff" -v error -y -f lavfi -i "life=size=320x180:rate=30:mold=10:ratio=0.1,scale=1280:720:flags=neighbor,format=yuv420p" \
			-f lavfi -i "sine=frequency=440:sample_rate=48000:duration=9" \
			-f lavfi -i "sine=frequency=220:sample_rate=48000:duration=9" \
			-t 9 -map 0:v -map 1:a -map 2:a -c:v libx264 -preset veryfast -crf 28 -c:a aac -b:a 96k \
			-metadata:s:a:0 title=Game -metadata:s:a:1 title=Mic "$p"
		echo "  $p"
	}
done

hk="$out/Hollow Knight"
# OBS MP4 with PCM audio: Firefox cannot decode it in <video>.
p="$hk/2026-04-10 23-12-45.mp4"
mkdir -p "$hk"
[[ -f $p ]] || {
	"$ff" -v error -y -f lavfi -i "gradients=size=1280x720:rate=30:speed=0.02,format=yuv420p" \
		-f lavfi -i "sine=frequency=262:sample_rate=48000:duration=10" \
		-f lavfi -i "anoisesrc=r=48000:a=0.05:d=10" \
		-t 10 -map 0:v -map 1:a -map 2:a -c:v libx264 -preset veryfast -crf 28 -c:a pcm_f32le -ac 2 "$p"
	echo "  $p"
}

lc="$out/Lethal Company"
clip "$lc/Lethal Company 2026.05.01 - 01.02.03.04.DVR.mp4" "smptehdbars=size=1280x720:rate=30" 6 "${h264[@]}"
clip "$lc/Lethal Company 2026.05.03 - 02.22.10.01.DVR.mp4" "mandelbrot=size=1280x720:rate=30:start_scale=0.5" 8 "${h264[@]}"
s="$lc/Lethal Company Screenshot 2026.05.01 - 01.05.00.00.png"
[[ -f $s ]] || { "$ff" -v error -y -f lavfi -i "testsrc2=size=1280x720" -frames:v 1 "$s"; echo "  $s"; }

drg="$out/Deep Rock Galactic"
clip "$drg/Deep Rock Galactic 2026.06.12 - 20.00.00.01.DVR.mp4" "cellauto=size=1280x720:rate=30:rule=30" 7 "${h264[@]}"

echo "done: $(find "$out" -type f | wc -l) files"
