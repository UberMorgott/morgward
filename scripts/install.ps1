#Requires -Version 5
<#
.SYNOPSIS
  morgward installer / runner for Windows — no manual download needed.

.DESCRIPTION
  Downloads the latest release binary (with a live progress bar), verifies its
  SHA-256 against the release checksums.txt, installs it under
  %LOCALAPPDATA%\Programs\morgward, and launches it.

.EXAMPLE
  # run straight from the internet (downloads + launches the TUI):
  irm https://raw.githubusercontent.com/UberMorgott/morgward/main/scripts/install.ps1 | iex

.NOTES
  Set $env:MORGWARD_NO_LAUNCH = '1' to install WITHOUT launching (e.g. for scripting/CI).
#>
$ErrorActionPreference = 'Stop'
# GitHub requires TLS 1.2+; Windows PowerShell 5.1 defaults lower, so force it.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch {}

$repo = 'UberMorgott/morgward'

# Windows release is amd64-only; Windows 11 on ARM runs it under x64 emulation.
$asset = 'morgward-windows-amd64.exe'
if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') {
    Write-Host 'note: no native windows/arm64 build — using amd64 (x64 emulation).' -ForegroundColor Yellow
}

$base    = "https://github.com/$repo/releases/latest/download"
$destDir = Join-Path $env:LOCALAPPDATA 'Programs\morgward'
$dest    = Join-Path $destDir 'morgward.exe'
New-Item -ItemType Directory -Force $destDir | Out-Null

# Streamed download with a real Write-Progress bar (so a slow connection still
# shows movement, unlike a silent blocking download).
function Get-WithProgress {
    param([string]$Url, [string]$OutFile, [string]$Activity)
    $req = [System.Net.HttpWebRequest]::Create($Url)
    $req.UserAgent       = 'morgward-installer'
    $req.AllowAutoRedirect = $true   # GitHub redirects /latest/download to the asset host
    $resp = $req.GetResponse()
    try {
        $total = [int64]$resp.ContentLength
        $in    = $resp.GetResponseStream()
        $out   = [System.IO.File]::Create($OutFile)
        try {
            $buf  = New-Object byte[] 131072
            $read = [int64]0
            while (($n = $in.Read($buf, 0, $buf.Length)) -gt 0) {
                $out.Write($buf, 0, $n)
                $read += $n
                if ($total -gt 0) {
                    $pct = [int](($read * 100) / $total)
                    $st  = '{0:N1} / {1:N1} MB' -f ($read / 1MB), ($total / 1MB)
                    Write-Progress -Activity $Activity -Status $st -PercentComplete $pct
                } else {
                    Write-Progress -Activity $Activity -Status ('{0:N1} MB' -f ($read / 1MB))
                }
            }
        } finally { $out.Close() }
    } finally { $resp.Close() }
    Write-Progress -Activity $Activity -Completed
}

Write-Host "morgward: downloading latest ($asset)..." -ForegroundColor Cyan
Get-WithProgress -Url "$base/$asset" -OutFile $dest -Activity 'Downloading morgward'

# Verify SHA-256 against the release checksums.txt — fail closed on a mismatch.
$sumsFile = Join-Path $destDir 'checksums.txt'
try {
    Get-WithProgress -Url "$base/checksums.txt" -OutFile $sumsFile -Activity 'Downloading checksums'
    $line = (Select-String -Path $sumsFile -Pattern ([regex]::Escape($asset)) | Select-Object -First 1).Line
    $want = if ($line) { ($line -split '\s+')[0].ToLower() } else { '' }
    $got  = (Get-FileHash -Algorithm SHA256 -LiteralPath $dest).Hash.ToLower()
    if ($want -and $got -eq $want) {
        Write-Host "morgward: checksum OK ($got)" -ForegroundColor Green
    } elseif ($want) {
        Remove-Item $dest -Force
        throw "checksum MISMATCH (got $got, want $want) — deleted download."
    } else {
        Write-Host 'morgward: checksum entry not found — skipping verification.' -ForegroundColor Yellow
    }
} catch {
    if (-not (Test-Path $dest)) { throw }
    Write-Host "morgward: checksum step skipped ($($_.Exception.Message))" -ForegroundColor Yellow
}

Write-Host "morgward: installed -> $dest" -ForegroundColor Green
Write-Host "re-run anytime:  & '$dest'      (or add '$destDir' to PATH)" -ForegroundColor DarkGray

if (-not $env:MORGWARD_NO_LAUNCH) {
    Write-Host 'morgward: launching...' -ForegroundColor Cyan
    & $dest
}
