# Rasterize icon.svg to PNG set via headless Chrome, then crop top-left to exact square.
# Chrome headless ignores --window-size here, so we render the SVG at $s px in the
# top-left of a transparent page and crop it out, preserving alpha.
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Drawing
$here   = Split-Path -Parent $MyInvocation.MyCommand.Path
$master = Join-Path $here "icon.svg"
$chrome = "C:\Program Files\Google\Chrome\Application\chrome.exe"
if (-not (Test-Path $chrome)) { $chrome = "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe" }

$svgBody = (Get-Content $master -Raw)
$sizes = 16, 32, 48, 64, 128, 256, 512

foreach ($s in $sizes) {
  $svgInline = $svgBody -replace 'width="256" height="256" ', ''
  $html = @"
<!doctype html><html><head><meta charset="utf-8"><style>
html,body{margin:0;padding:0;background:transparent}
svg{display:block;width:${s}px;height:${s}px}
</style></head><body>$svgInline</body></html>
"@
  $tmp = Join-Path $here "_tmp_$s.html"
  $raw = Join-Path $here "_raw_$s.png"
  $out = Join-Path $here "icon-$s.png"
  Set-Content -Path $tmp -Value $html -Encoding UTF8
  & $chrome --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 `
            --default-background-color=00000000 `
            --screenshot="$raw" "file:///$($tmp -replace '\\','/')" 2>$null | Out-Null
  Remove-Item $tmp -Force

  $src  = [System.Drawing.Image]::FromFile($raw)
  $dst  = New-Object System.Drawing.Bitmap($s, $s, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $g    = [System.Drawing.Graphics]::FromImage($dst)
  $g.DrawImage($src, (New-Object System.Drawing.Rectangle(0,0,$s,$s)), 0,0,$s,$s, [System.Drawing.GraphicsUnit]::Pixel)
  $g.Dispose(); $src.Dispose()
  $dst.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
  $dst.Dispose()
  Remove-Item $raw -Force
  Write-Host ("icon-{0}.png  {1}x{1}  {2:N0} bytes" -f $s, $s, (Get-Item $out).Length)
}
