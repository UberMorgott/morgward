# Cross-compile morgward for all supported targets into ./dist
$ErrorActionPreference = 'Stop'
$targets = @(
    @{ os = 'linux';   arch = 'amd64'; ext = '' },
    @{ os = 'linux';   arch = 'arm64'; ext = '' },
    @{ os = 'darwin';  arch = 'amd64'; ext = '' },
    @{ os = 'darwin';  arch = 'arm64'; ext = '' },
    @{ os = 'windows'; arch = 'amd64'; ext = '.exe' }
)
New-Item -ItemType Directory -Force dist | Out-Null
foreach ($t in $targets) {
    $out = "dist/morgward-$($t.os)-$($t.arch)$($t.ext)"
    $env:GOOS = $t.os; $env:GOARCH = $t.arch
    Write-Host "building $out"
    go build -trimpath -ldflags '-s -w' -o $out ./cmd/morgward
}
Remove-Item Env:GOOS, Env:GOARCH

# Emit dist/checksums.txt in the sha256sum format go-selfupdate's ChecksumValidator
# parses: lowercase hex sha256, two spaces, then the bare artifact filename.
$lines = foreach ($f in Get-ChildItem -Path dist -File -Filter 'morgward-*' | Sort-Object Name) {
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $f.FullName).Hash.ToLower()
    "$hash  $($f.Name)"
}
Set-Content -Path 'dist/checksums.txt' -Value $lines -Encoding ascii -NoNewline:$false

# Linux desktop-integration tarballs (binary + .desktop + hicolor icons + install).
# Emitted under dist/desktop/ so they never enter the dist/morgward-* checksum glob
# above (the go-selfupdate contract). File modes are normalized by install.sh at
# install time (install -Dm755/-Dm644), so Windows-built tarballs need no chmod.
New-Item -ItemType Directory -Force dist/desktop | Out-Null
foreach ($arch in @('amd64', 'arm64')) {
    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ("mw-" + [System.Guid]::NewGuid().ToString('N'))
    $root  = Join-Path $stage 'morgward'
    New-Item -ItemType Directory -Force $root | Out-Null
    Copy-Item "dist/morgward-linux-$arch"        (Join-Path $root 'morgward')
    Copy-Item 'packaging/linux/morgward.desktop' $root
    Copy-Item 'packaging/linux/install.sh'       $root
    Copy-Item 'packaging/linux/uninstall.sh'     $root
    Copy-Item 'packaging/linux/icons'            $root -Recurse
    tar.exe -C $stage -czf "dist/desktop/morgward-linux-$arch-desktop.tar.gz" morgward
    Remove-Item $stage -Recurse -Force
    Write-Host "packaged dist/desktop/morgward-linux-$arch-desktop.tar.gz"
}
Write-Host "done -> ./dist"
