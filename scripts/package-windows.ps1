[CmdletBinding()]
param(
    [ValidateSet('arm64', 'amd64', 'all')]
    [string]$Arch = 'all',

    [string]$Out = '',

    [string]$Tag = '',

    [string]$Sidecar = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo = 'Eslzzyl/krkrgo'
$Root = Split-Path -Parent $PSScriptRoot
$SidecarLibraries = @('avutil-61.dll', 'avcodec-63.dll', 'avformat-63.dll', 'swscale-10.dll')
$VersionFile = Join-Path $Root 'internal/version/version.xcconfig'
$VersionValues = @{}
if (-not (Test-Path -LiteralPath $VersionFile)) {
    throw "version file is missing: $VersionFile"
}
foreach ($line in Get-Content -LiteralPath $VersionFile) {
    if ($line -match '^\s*([A-Z0-9_]+)\s*=\s*(\S+)\s*$') {
        $VersionValues[$Matches[1]] = $Matches[2]
    }
}
$AppVersion = $VersionValues['KRKRGO_VERSION']
$AppBuild = $VersionValues['KRKRGO_BUILD']
if ([string]::IsNullOrWhiteSpace($AppVersion) -or [string]::IsNullOrWhiteSpace($AppBuild)) {
    throw "version file is missing KRKRGO_VERSION or KRKRGO_BUILD"
}

function Fail {
    param([string]$Message)
    Write-Error "package: $Message" -ErrorAction Stop
}

function Resolve-SidecarTag {
    param([string]$Requested)

    if ([string]::IsNullOrWhiteSpace($Requested)) {
        $Requested = $env:PKG_TAG
    }
    if (-not [string]::IsNullOrWhiteSpace($Requested)) {
        return $Requested
    }
    $tag = gh release list --repo $Repo --limit 100 --json tagName --jq '.[].tagName' |
        Where-Object { $_ -like 'ffmpeg-sidecar-*' } |
        Select-Object -First 1
    if ([string]::IsNullOrWhiteSpace($tag)) {
        Fail 'no ffmpeg-sidecar release found'
    }
    return $tag
}

$Tag = Resolve-SidecarTag -Requested $Tag

function Resolve-OutputDirectory {
    param([string]$Path)

    if ([string]::IsNullOrEmpty($Path)) {
        $Path = Join-Path $Root 'dist'
    } elseif (-not [System.IO.Path]::IsPathRooted($Path)) {
        $Path = Join-Path (Get-Location).Path $Path
    }

    New-Item -ItemType Directory -Force -Path $Path | Out-Null
    return (Resolve-Path -LiteralPath $Path).Path
}

function Get-SidecarArch {
    param([string]$GoArch)

    switch ($GoArch) {
        'arm64' { return 'arm64' }
        'amd64' { return 'x86_64' }
        default { Fail "unsupported architecture: $GoArch" }
    }
}

function Get-SidecarDirectory {
    param(
        [string]$GoArch,
        [string]$ReleaseTag
    )

    $ffArch = Get-SidecarArch $GoArch
    $cache = Join-Path (Join-Path $Root 'temp') 'sidecars'
    $cache = Join-Path $cache $ReleaseTag
    $sidecarDir = Join-Path $cache "ffmpeg-windows-$ffArch"

    if (-not (Test-Path -LiteralPath $sidecarDir)) {
        New-Item -ItemType Directory -Force -Path $cache | Out-Null
        $asset = "$ReleaseTag-windows-$ffArch.zip"
        $url = "https://github.com/$Repo/releases/download/$ReleaseTag/$asset"
        $archive = Join-Path $cache $asset
        Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
        Expand-Archive -LiteralPath $archive -DestinationPath $cache -Force
    }

    if (-not (Test-Path -LiteralPath $sidecarDir)) {
        Fail "sidecar archive layout is unexpected: $sidecarDir"
    }

    return $sidecarDir
}

function Assert-Sidecar {
    param([string]$Directory)

    $libDir = Join-Path (Join-Path $Directory 'lib') 'ffmpeg'
    foreach ($lib in $SidecarLibraries) {
        $path = Join-Path $libDir $lib
        if (-not (Test-Path -LiteralPath $path)) {
            Fail "sidecar library is missing: $path"
        }
    }

    $license = Join-Path (Join-Path (Join-Path $Directory 'LICENSES') 'FFmpeg') 'COPYING.LGPLv2.1'
    if (-not (Test-Path -LiteralPath $license)) {
        Fail "sidecar license is missing: $license"
    }
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail 'required command is missing: go'
}

$architectures = @()
if ($Arch -eq 'all') {
    $architectures = @('arm64', 'amd64')
} else {
    $architectures = @($Arch)
}

if (-not [string]::IsNullOrEmpty($Sidecar) -and $architectures.Count -gt 1) {
    Fail '-Sidecar requires a single architecture'
}

$OutputDir = Resolve-OutputDirectory $Out

foreach ($goArch in $architectures) {
    Write-Host "packaging Windows $goArch"

    if (-not [string]::IsNullOrEmpty($Sidecar)) {
        $sidecarDir = (Resolve-Path -LiteralPath $Sidecar).Path
    } else {
        $sidecarDir = Get-SidecarDirectory $goArch $Tag
    }
    Assert-Sidecar $sidecarDir

    $stageRoot = Join-Path (Join-Path $Root 'temp') 'package'
    $stage = Join-Path $stageRoot "windows-$goArch"
    $packageName = "krkrgo-windows-$goArch"
    $packageDir = Join-Path $stage $packageName

    if (Test-Path -LiteralPath $stage) {
        Remove-Item -LiteralPath $stage -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $packageDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $packageDir 'LICENSES') | Out-Null

    $exe = Join-Path $packageDir 'krkrgo.exe'
    Push-Location $Root
    try {
        $env:CGO_ENABLED = '0'
        $env:GOOS = 'windows'
        $env:GOARCH = $goArch
        $ldFlags = "-s -w -H windowsgui -X github.com/eslzzyl/krkrgo/internal/version.Version=$AppVersion -X github.com/eslzzyl/krkrgo/internal/version.Build=$AppBuild"
        & go build -trimpath -ldflags $ldFlags -o $exe ./cmd/krkrgo
        if ($LASTEXITCODE -ne 0) {
            Fail "go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }

    $sidecarLibDir = Join-Path (Join-Path $sidecarDir 'lib') 'ffmpeg'
    Copy-Item -Path (Join-Path $sidecarLibDir '*.dll') -Destination $packageDir -Force
    Copy-Item -LiteralPath (Join-Path (Join-Path $sidecarDir 'LICENSES') 'FFmpeg') -Destination (Join-Path $packageDir 'LICENSES') -Recurse -Force

    $artifact = Join-Path $OutputDir "$packageName.zip"
    if (Test-Path -LiteralPath $artifact) {
        Remove-Item -LiteralPath $artifact -Force
    }
    Compress-Archive -Path $packageDir -DestinationPath $artifact -CompressionLevel Optimal
    $hash = (Get-FileHash -LiteralPath $artifact -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -LiteralPath "${artifact}.sha256" -Value "$hash  $(Split-Path -Leaf $artifact)" -Encoding Ascii
    Write-Host "created $artifact"
}
