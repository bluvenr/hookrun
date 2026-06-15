param(
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

# Auto-detect version from git tag
if (-not $Version) {
    try {
        $Version = (git describe --tags --always 2>$null) -replace '^v', ''
    } catch {
        $Version = "dev"
    }
}

$BuildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$Flags = "-X github.com/bluvenr/hookrun/internal/version.Version=$Version -X 'github.com/bluvenr/hookrun/internal/version.BuildTime=$BuildTime' -s -w"
$OutDir = "dist"
$Binary = "hookrun"
$Entry = "./cmd/hookrun"

Write-Host "==> Building HookRun v$Version"
Write-Host "    Build time: $BuildTime"
Write-Host "    Output:     $OutDir/"
Write-Host ""

# Clean output
if (Test-Path $OutDir) { Remove-Item -Recurse -Force $OutDir }
New-Item -ItemType Directory -Force $OutDir | Out-Null

# Targets: [OS, Arch, Extension]
$Targets = @(
    @("linux",   "amd64", ""),
    @("linux",   "arm64", ""),
    @("darwin",  "amd64", ""),
    @("darwin",  "arm64", ""),
    @("windows", "amd64", ".exe")
)

foreach ($t in $Targets) {
    $GOOS   = $t[0]
    $GOARCH = $t[1]
    $Ext    = $t[2]

    $Platform = "$GOOS-$GOARCH"
    $Output   = "$OutDir/$Binary-$Platform$Ext"
    $PkgName  = "$Binary-v$Version-$Platform"

    Write-Host -NoNewline "  Building $Platform..."
    $env:GOOS   = $GOOS
    $env:GOARCH = $GOARCH
    go build -ldflags $Flags -trimpath -o $Output $Entry
    if ($LASTEXITCODE -ne 0) {
        Write-Host " FAILED" -ForegroundColor Red
        exit 1
    }
    Write-Host " done"

    # Package
    if ($GOOS -eq "windows") {
        $ZipFile = "$OutDir/$PkgName.zip"
        Write-Host -NoNewline "  Packaging $PkgName.zip..."
        Compress-Archive -Path $Output -DestinationPath $ZipFile -Force
        Remove-Item $Output
        Write-Host " done"
    } else {
        $TarFile = "$OutDir/$PkgName.tar.gz"
        Write-Host -NoNewline "  Packaging $PkgName.tar.gz..."
        Push-Location $OutDir
        tar -czf "$PkgName.tar.gz" "$Binary-$Platform"
        Pop-Location
        Remove-Item $Output
        Write-Host " done"
    }
}

# Clean env
Remove-Item Env:GOOS   -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "==> Build complete!"
Write-Host ""
Get-ChildItem $OutDir | Format-Table Name, Length -AutoSize
