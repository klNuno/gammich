<#
.SYNOPSIS
  Generate gammich preview sprites + thumbnails using local FFmpeg (NVDEC), then
  POST them to a running gammich server.

.DESCRIPTION
  Pulls the list of clips with preview_status in (pending, failed) from the
  gammich server, then for each one:
    1. Resolves the clip to a local path (the NAS share mounted on this PC)
    2. Runs ffmpeg twice (sprite 10-tile + thumbnail)
    3. POSTs both JPEGs as multipart/form-data to /api/ingest/preview

  Designed to run on a workstation with an NVIDIA GPU and the recordings share
  mapped locally. FFmpeg with NVDEC decoding is much faster than the CPU worker
  of a small server, which helps to index a large backlog once.

.PARAMETER Server
  Base URL of the gammich server, e.g. http://nas.local:8080
  Default: http://localhost:8080

.PARAMETER Token
  Value of GAMMICH_INGEST_TOKEN set on the server.

.PARAMETER VideoRoot
  Local path that maps to the same root the server scans. E.g. if the server
  scans //nas/share/Videos and you mounted the share as Z:\, pass Z:\Videos.

.PARAMETER FfmpegPath
  Path to ffmpeg.exe. Defaults to "ffmpeg" (PATH).

.PARAMETER FfprobePath
  Path to ffprobe.exe. Defaults to "ffprobe" (PATH).

.PARAMETER Limit
  Max clips to process this run. Default 100.

.PARAMETER WorkDir
  Temp dir for sprite/thumb JPEGs before upload. Default $env:TEMP\gammich.

.EXAMPLE
  pwsh ./scripts/gen-previews.ps1 -Server http://nas.local:8080 `
    -Token $env:GAMMICH_INGEST_TOKEN `
    -VideoRoot 'Z:\Videos' -Limit 500
#>

[CmdletBinding()]
param(
	[string]$Server = 'http://localhost:8080',
	[Parameter(Mandatory = $true)] [string]$Token,
	[Parameter(Mandatory = $true)] [string]$VideoRoot,
	[string]$FfmpegPath = 'ffmpeg',
	[string]$FfprobePath = 'ffprobe',
	[int]$Limit = 100,
	[string]$WorkDir = (Join-Path $env:TEMP 'gammich')
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

if (-not (Test-Path $WorkDir)) {
	New-Item -ItemType Directory -Force $WorkDir | Out-Null
}

function Get-MediaInfo {
	param([string]$Path)
	$out = & $FfprobePath -v error -select_streams 'v:0' `
		-show_entries 'stream=codec_name,width,height:format=duration' `
		-of default=nw=1 -- $Path 2>&1
	$info = @{ codec = ''; width = 0; height = 0; duration = 0; audio_tracks = 0 }
	foreach ($line in $out) {
		if ($line -match '^(\w+)=(.*)$') {
			switch ($matches[1]) {
				'codec_name' { $info.codec = $matches[2] }
				'width' { $info.width = [int]$matches[2] }
				'height' { $info.height = [int]$matches[2] }
				'duration' { $info.duration = [double]$matches[2] }
			}
		}
	}
	$audio = & $FfprobePath -v error -select_streams a -show_entries stream=index -of 'csv=p=0' -- $Path 2>$null
	$info.audio_tracks = @($audio | Where-Object { "$_".Trim() }).Count
	return $info
}

function Make-Sprite {
	param([string]$Src, [string]$Dst, [double]$Duration, [int]$Frames = 10, [int]$W = 320, [int]$H = 180)
	# Time-stepping mirrors the Go worker (preview.go genSprite): frame counts
	# are unreliable on AV1 VFR / open-GOP streams.
	if ($Duration -le 0) { $Duration = 1 }
	$step = $Duration / ($Frames + 1)
	if ($step -lt 0.01) { $step = 0.01 }
	$stepStr = $step.ToString('0.0000', [cultureinfo]::InvariantCulture)
	$vf = "fps=1/$stepStr,scale=${W}:${H}:force_original_aspect_ratio=decrease,pad=${W}:${H}:(ow-iw)/2:(oh-ih)/2,tile=${Frames}x1"
	& $FfmpegPath -y -v error -hwaccel cuda -i $Src -vf $vf -frames:v 1 -q:v 4 $Dst
	if ($LASTEXITCODE -ne 0) { throw "ffmpeg sprite failed for $Src" }
}

function Make-Thumb {
	param([string]$Src, [string]$Dst, [double]$Duration, [int]$W = 640, [int]$H = 360)
	$seek = if ($Duration -lt 2) { 0.0 } else { $Duration * 0.5 }
	$seekStr = $seek.ToString('0.000', [cultureinfo]::InvariantCulture)
	$vf = "scale=${W}:${H}:force_original_aspect_ratio=decrease"
	& $FfmpegPath -y -v error -ss $seekStr -hwaccel cuda -i $Src -frames:v 1 -vf $vf -q:v 3 $Dst
	if ($LASTEXITCODE -ne 0) { throw "ffmpeg thumb failed for $Src" }
}

function Post-Preview {
	param(
		[long]$ClipId,
		[string]$SpritePath,
		[string]$ThumbPath,
		[hashtable]$Info
	)
	$boundary = [Guid]::NewGuid().ToString('N')
	$LF = "`r`n"

	$payload = New-Object System.IO.MemoryStream
	$writer = New-Object System.IO.BinaryWriter($payload)

	function Add-Field([string]$name, [string]$value) {
		$head = "--$boundary$LF" +
		"Content-Disposition: form-data; name=`"$name`"$LF$LF" +
		"$value$LF"
		$writer.Write([Text.Encoding]::UTF8.GetBytes($head))
	}
	function Add-File([string]$name, [string]$path, [string]$ctype = 'image/jpeg') {
		$fname = Split-Path $path -Leaf
		$head = "--$boundary$LF" +
		"Content-Disposition: form-data; name=`"$name`"; filename=`"$fname`"$LF" +
		"Content-Type: $ctype$LF$LF"
		$writer.Write([Text.Encoding]::UTF8.GetBytes($head))
		$bytes = [IO.File]::ReadAllBytes($path)
		$writer.Write($bytes)
		$writer.Write([Text.Encoding]::UTF8.GetBytes($LF))
	}

	Add-Field 'clip_id' ([string]$ClipId)
	Add-Field 'duration_s' ([string]$Info.duration)
	Add-Field 'width' ([string]$Info.width)
	Add-Field 'height' ([string]$Info.height)
	Add-Field 'codec' ($Info.codec ?? '')
	Add-Field 'audio_track_count' ([string]$Info.audio_tracks)
	Add-File 'sprite' $SpritePath
	Add-File 'thumb' $ThumbPath

	$writer.Write([Text.Encoding]::UTF8.GetBytes("--$boundary--$LF"))
	$writer.Flush()
	$bytes = $payload.ToArray()
	$writer.Dispose()
	$payload.Dispose()

	$headers = @{ 'X-Gammich-Token' = $Token; 'Content-Type' = "multipart/form-data; boundary=$boundary" }
	Invoke-RestMethod -Method POST -Uri "$Server/api/ingest/preview" -Headers $headers -Body $bytes | Out-Null
}

Write-Host "==> Fetching pending list ($Server, limit=$Limit)"
$headers = @{ 'X-Gammich-Token' = $Token }
$pending = Invoke-RestMethod -Uri "$Server/api/ingest/pending?limit=$Limit" -Headers $headers
if (-not $pending) {
	Write-Host 'Nothing to do.'
	return
}

Write-Host "==> $($pending.Count) clips to process"
$i = 0
foreach ($p in $pending) {
	$i++
	$rel = $p.rel_path -replace '/', '\'
	$src = Join-Path $VideoRoot $rel
	if (-not (Test-Path $src)) {
		Write-Warning "[$i/$($pending.Count)] missing $src"
		continue
	}
	$sprite = Join-Path $WorkDir ("sprite-$($p.id).jpg")
	$thumb = Join-Path $WorkDir ("thumb-$($p.id).jpg")
	Write-Host "[$i/$($pending.Count)] $($p.id) :: $($p.rel_path)"
	try {
		$info = Get-MediaInfo -Path $src
		Make-Sprite -Src $src -Dst $sprite -Duration $info.duration
		Make-Thumb -Src $src -Dst $thumb -Duration $info.duration
		Post-Preview -ClipId $p.id -SpritePath $sprite -ThumbPath $thumb -Info $info
		Remove-Item $sprite, $thumb -ErrorAction SilentlyContinue
	} catch {
		Write-Warning "  -> $($_.Exception.Message)"
	}
}
Write-Host '==> Done.'
