# Run from an extracted Windows release or a source checkout.
# Forward all arguments verbatim to the native setup command.
$ErrorActionPreference = 'Stop'
$rootPath = $PSScriptRoot
$releaseBinary = Join-Path $rootPath 'itmo-calendar-sync.exe'
if (Test-Path -LiteralPath $releaseBinary -PathType Leaf) {
    & $releaseBinary setup @args
    exit $LASTEXITCODE
}
if (-not (Test-Path -LiteralPath (Join-Path $rootPath 'go.mod') -PathType Leaf)) {
    throw 'The release executable is missing. Extract the entire Windows release archive.'
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Install Go 1.26 or newer to build from source, or download a prebuilt release archive.'
}
Push-Location $rootPath
try {
    New-Item -ItemType Directory -Force -Path 'bin' | Out-Null
    & go build -trimpath -o 'bin/itmo-calendar-sync.exe' './cmd/itmo-calendar-sync'
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
} finally {
    Pop-Location
}
& (Join-Path $rootPath 'bin/itmo-calendar-sync.exe') setup @args
exit $LASTEXITCODE
