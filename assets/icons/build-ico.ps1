# Pack PNGs into a multi-resolution favicon.ico (PNG-compressed entries, Vista+).
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$sizes = 16, 32, 48, 256
$pngs  = $sizes | ForEach-Object { Join-Path $here "icon-$_.png" }

$ms = New-Object System.IO.MemoryStream
$bw = New-Object System.IO.BinaryWriter($ms)
# ICONDIR header
$bw.Write([UInt16]0); $bw.Write([UInt16]1); $bw.Write([UInt16]$sizes.Count)

$blobs  = $pngs | ForEach-Object { ,([System.IO.File]::ReadAllBytes($_)) }   # ,() keeps each byte[] as one element (no pipeline unroll)
$offset = 6 + 16 * $sizes.Count
for ($i = 0; $i -lt $sizes.Count; $i++) {
  $s = $sizes[$i]; $len = $blobs[$i].Length
  $bw.Write([Byte]($(if ($s -ge 256) { 0 } else { $s })))   # width  (0 = 256)
  $bw.Write([Byte]($(if ($s -ge 256) { 0 } else { $s })))   # height (0 = 256)
  $bw.Write([Byte]0); $bw.Write([Byte]0)                    # palette, reserved
  $bw.Write([UInt16]1); $bw.Write([UInt16]32)               # planes, bpp
  $bw.Write([UInt32]$len); $bw.Write([UInt32]$offset)       # size, offset
  $offset += $len
}
foreach ($blob in $blobs) { $bw.Write($blob) }
$bw.Flush()
[System.IO.File]::WriteAllBytes((Join-Path $here "favicon.ico"), $ms.ToArray())
$bw.Dispose(); $ms.Dispose()
Write-Host ("favicon.ico  {0:N0} bytes  ({1})" -f (Get-Item (Join-Path $here 'favicon.ico')).Length, ($sizes -join '/'))
