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
Write-Host "done -> ./dist"
