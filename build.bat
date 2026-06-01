@echo off
rem Cross-compile morgward for all supported targets into .\dist (production build).
rem Mirror of build.ps1 / `make release`. Run from the repo root: build.bat
setlocal enabledelayedexpansion

set "LDFLAGS=-s -w"
set "PKG=.\cmd\morgward"
set "BINARY=morgward"

if not exist dist mkdir dist

rem target list: "GOOS GOARCH ext"
for %%T in (
    "linux amd64 ."
    "linux arm64 ."
    "darwin amd64 ."
    "darwin arm64 ."
    "windows amd64 .exe"
) do (
    for /f "tokens=1-3" %%A in (%%T) do (
        set "GOOS=%%A"
        set "GOARCH=%%B"
        set "EXT=%%C"
        if "!EXT!"=="." set "EXT="
        set "OUT=dist\%BINARY%-%%A-%%B!EXT!"
        echo building !OUT!
        go build -trimpath -ldflags "%LDFLAGS%" -o "!OUT!" "%PKG%"
        if errorlevel 1 (
            echo BUILD FAILED for %%A/%%B
            endlocal
            exit /b 1
        )
    )
)

set "GOOS="
set "GOARCH="
echo done -^> .\dist
endlocal
