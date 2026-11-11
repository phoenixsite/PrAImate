# PrAImate installer for Windows.
#
# One-liner:
#   iwr -useb https://raw.githubusercontent.com/sPROFFEs/praimate/main/scripts/install.ps1 | iex
#
# Or with options (you must download then run for arguments to bind):
#   iwr -useb https://… -OutFile install.ps1
#   .\install.ps1 -Mode Source -AllUsers
#
# Or run locally (from inside an extracted release archive or repo checkout):
#   .\scripts\install.ps1
#
# Parameters:
#   -Edition Desktop | Server  edition (default: Desktop)
#   -Mode Binary | Source       what to install (prompts if omitted)
#   -Prefix <dir>               install dir (default: %LOCALAPPDATA%\Programs\PrAImate)
#   -AllUsers                   install to %ProgramFiles%\PrAImate (needs admin)
#   -Yes                        auto-confirm prompts
#   -Uninstall                  remove binaries, shortcuts and the PATH entry
#                               (config, chats and the DB are kept)
#   -Uninstall -Purge           ALSO delete config, managed tools and the DB
#
# The binary path resolves the latest GitHub release via the API.
# Set $env:RELEASE_TAG=<tag> to pin a specific release instead.

[CmdletBinding()]
param(
    [ValidateSet("Binary","Source","")]
    [string]$Mode = "",
    [ValidateSet("Desktop", "Server")]
    [string]$Edition = "Desktop",
    [string]$Prefix = "",
    [switch]$AllUsers,
    [switch]$Yes,
    [switch]$Uninstall,
    [switch]$Purge
)

$ErrorActionPreference = "Stop"

# Windows PowerShell 5.1's .NET web stack can default to TLS 1.0, which
# GitHub rejects. Opt into TLS 1.2 and 1.3 where the OS supports it.
try {
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor 12288
} catch {}
# PowerShell 5.1's progress rendering makes large downloads much slower.
$ProgressPreference = "SilentlyContinue"

$Repo = "sPROFFEs/praimate"
$GitHubURL = "https://github.com"
$RepoURL = "$GitHubURL/$Repo"
$ReleaseApiURL = "https://api.github.com/repos/$Repo/releases/latest"

# ---------- pretty ----------
function Step($Text) { Write-Host ""; Write-Host "==> $Text" -ForegroundColor Green }
function Info($Text) { Write-Host "    $Text" -ForegroundColor DarkGray }
function Warn($Text) { Write-Host $Text -ForegroundColor Yellow }
function Fail($Text) { Write-Host $Text -ForegroundColor Red; exit 1 }

# ---------- prompts that work even from iwr | iex ----------
# `iwr | iex` runs the script in the host PowerShell, so Read-Host
# still works. But $Host.UI may be a non-interactive host (PSExec,
# CI). Detect once and route every prompt through this.
$IsInteractive = -not ([Console]::IsInputRedirected) -and ($Host.Name -ne "Default Host")

function Ask($PromptText, $Default) {
    if ($Yes) { return $Default }
    if (-not $IsInteractive) { return $Default }
    $reply = Read-Host "$PromptText [$Default]"
    if ([string]::IsNullOrWhiteSpace($reply)) { return $Default }
    return $reply.Trim()
}

function YesNo($PromptText, $DefaultYes = $true) {
    $hint = if ($DefaultYes) { "[Y/n]" } else { "[y/N]" }
    $default = if ($DefaultYes) { "y" } else { "n" }
    $r = (Ask "$PromptText $hint" $default).ToLower()
    return $r -match "^y(es)?$"
}

# ---------- platform detect ----------
function Get-ArchTriplet {
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        "AMD64" { return "windows-amd64" }
        "ARM64" { return "windows-arm64" }
        default { Fail "unsupported PROCESSOR_ARCHITECTURE: $arch" }
    }
}
$Triplet = Get-ArchTriplet

# ---------- uninstall ----------
if ($Uninstall) {
    $dests = @()
    if ($Prefix) { $dests += $Prefix }
    else {
        $dests += (Join-Path $env:LOCALAPPDATA "Programs\PrAImate")
        $dests += (Join-Path $env:ProgramFiles "PrAImate")
    }
    $removed = $false
    foreach ($d in $dests) {
        if (-not (Test-Path $d)) { continue }
        Step "Removing binaries from $d"
        foreach ($f in @("praimate.exe","wpc.exe","praimate-gui.exe","praimate-edition","praimate-code.exe","praimate-cli.exe","praimate-cli.exe.old",
                         "praimate.exe.old","praimate-gui.exe.old","praimate-code.exe.old",
                         "PRAIMATE-CODE-LICENSE","PRAIMATE-CODE-NOTICE")) {
            $p = Join-Path $d $f
            if (Test-Path $p) { Remove-Item -Force $p; Info "removed $f"; $removed = $true }
        }
        $share = Join-Path (Split-Path $d -Parent) "share\praimate"
        if (Test-Path $share) { Remove-Item -Recurse -Force $share; Info "removed $share" }
        if ((Get-ChildItem $d -Force -ErrorAction SilentlyContinue | Measure-Object).Count -eq 0) {
            Remove-Item -Force $d
        }
        foreach ($scope in @("User","Machine")) {
            try {
                $cur = [Environment]::GetEnvironmentVariable("PATH", $scope)
                if ($cur) {
                    $had = $false
                    $parts = @()
                    foreach ($p in ($cur -split ";")) {
                        if ($p -and ($p.TrimEnd('\') -ieq $d.TrimEnd('\'))) { $had = $true }
                        else { $parts += $p }
                    }
                    if ($had) {
                        [Environment]::SetEnvironmentVariable("PATH", ($parts -join ";"), $scope)
                        Info "removed $d from $scope PATH"
                    }
                }
            } catch {}
        }
    }
    Step "Removing shortcuts"
    $desk = [Environment]::GetFolderPath("Desktop")
    $startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
    $ws = New-Object -ComObject WScript.Shell
    foreach ($n in @("PrAImate.lnk","PrAImate GUI.lnk")) {
        foreach ($dir in @($desk, $startMenu)) {
            $lnk = Join-Path $dir $n
            if (-not (Test-Path $lnk)) { continue }
            $target = ""
            try { $target = $ws.CreateShortcut($lnk).TargetPath } catch {}
            $mine = $false
            foreach ($d in $dests) {
                if ($target -and $target.StartsWith($d.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) {
                    $mine = $true
                }
            }
            if ($mine) { Remove-Item -Force $lnk; Info "removed $lnk" }
        }
    }
    if ($Purge) {
        Step "Purging config + data"
        foreach ($p in @((Join-Path $env:APPDATA "praimate"),
                         (Join-Path $env:APPDATA "PrAImate"),
                         (Join-Path $env:APPDATA "clade"),
                         (Join-Path $env:LOCALAPPDATA "praimate"))) {
            if (Test-Path $p) { Remove-Item -Recurse -Force $p; Info "purged $p" }
        }
    } else {
        Info "(config, managed tools and the chat DB were kept - add -Purge to remove them)"
    }
    if ($removed) { Step "PrAImate uninstalled" } else { Warn "nothing to remove (already uninstalled?)" }
    exit 0
}

# ---------- locate local binaries (release-archive / repo case) ----------
$here = if ($MyInvocation.MyCommand.Path) {
    Split-Path -Parent $MyInvocation.MyCommand.Path
} else { $null }

function Find-LocalBins {
    $cands = @($PWD.Path)
    if ($here) {
        $cands += $here
        $cands += (Join-Path $here "..")
        $cands += (Join-Path (Join-Path $here "..") (Join-Path "dist" $Triplet))
    }
    foreach ($c in $cands) {
        if ([string]::IsNullOrWhiteSpace($c)) { continue }
        $praimate = Join-Path $c "praimate.exe"
        $wpc   = Join-Path $c "wpc.exe"
        if ((Test-Path $praimate) -and (Test-Path $wpc)) {
            return (Resolve-Path $c).Path
        }
    }
    return $null
}
$LocalBins = Find-LocalBins

# ---------- mode prompt ----------
if (-not $Mode) {
    if ($LocalBins) {
        Info "(found local binaries in $LocalBins - skipping download/build prompt)"
        $Mode = "Local"
    } else {
        Write-Host ""
        Write-Host "How do you want to install PrAImate?"
        Write-Host "  1. Download a prebuilt release"
        Write-Host "  2. Build from source (needs Go; will offer to install Go if missing)"
        Write-Host "  3. Cancel"
        $choice = Ask "Choose 1, 2, or 3" "1"
        switch ($choice) {
            "1" { $Mode = "Binary" }
            "2" { $Mode = "Source" }
            "3" { Warn "cancelled."; exit 0 }
            default { Fail "invalid choice: $choice" }
        }
    }
}

# ---------- destination ----------
function Choose-Dest {
    if ($Prefix) { return $Prefix }
    if ($AllUsers) {
        return (Join-Path $env:ProgramFiles "PrAImate")
    }
    return (Join-Path $env:LOCALAPPDATA "Programs\PrAImate")
}
$Dest = Choose-Dest
# Keep a pre-existing installation in its edition unless explicitly selected.
$EditionMarker = Join-Path $Dest "praimate-edition"
if (-not $PSBoundParameters.ContainsKey('Edition') -and (Test-Path $EditionMarker -PathType Leaf)) {
    $Edition = (Get-Content -Raw $EditionMarker).Trim()
}
if ($Edition.ToLowerInvariant() -notin @('desktop', 'server')) { throw 'Invalid installed edition marker' }
$Edition = $Edition.ToLowerInvariant()
$SourceBranch = if ($Edition -eq "server") { "server" } else { "main" }
$AssetPrefix = if ($Edition -eq "server") { "praimate-server" } else { "praimate" }
$BuildTags = if ($Edition -eq "server") { "server" } else { "" }
$RootBuildArgs = @()
if ($BuildTags) { $RootBuildArgs = @("-tags", $BuildTags) }
$GuiBuildTags = if ($BuildTags) { "desktop,production,server" } else { "desktop,production" }
# Release tag to pull assets from. When unset we resolve "latest" via
# the GitHub API at download time so the installer keeps working as
# the operator publishes new versioned tags (1.0.8, 1.0.9, ...).
# Override with $env:RELEASE_TAG=<tag> to pin a specific release.
$ReleaseTag = $env:RELEASE_TAG
if ($ReleaseTag) {
    $tagPattern = if ($Edition -eq "server") { "^(server-)?v[0-9]" } else { "^v[0-9]" }
    if ($ReleaseTag -notmatch $tagPattern) { throw "Release tag $ReleaseTag does not belong to the $Edition edition" }
}

if (-not (Test-Path $Dest)) {
    New-Item -ItemType Directory -Path $Dest -Force | Out-Null
}

# Touching %ProgramFiles% needs admin. Detect early.
function Test-IsAdmin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $p  = New-Object Security.Principal.WindowsPrincipal($id)
    return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
if ($AllUsers -and -not (Test-IsAdmin)) {
    Fail "-AllUsers needs an elevated PowerShell. Re-open as Administrator and re-run."
}

function Assert-BundleEdition($Bundle) {
    $marker = Join-Path $Bundle "praimate-edition"
    $actual = if (Test-Path $marker) { (Get-Content -Raw $marker).Trim() } else { "desktop" }
    if ($actual -ne $Edition) {
        throw "This is a $actual bundle, but $Edition was requested. No binaries were replaced."
    }
    foreach ($name in @("praimate.exe", "wpc.exe", "praimate-gui.exe")) {
        if (-not (Test-Path (Join-Path $Bundle $name) -PathType Leaf)) {
            throw "Bundle is missing mandatory executable $name. No binaries were replaced."
        }
    }
}

# ---------- binary path: GitHub release ----------
function Resolve-LatestTag {
    if ($Edition -eq "server") {
        $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=100" `
            -UseBasicParsing -Headers @{ "User-Agent" = "praimate-installer" } -ErrorAction Stop
        $serverRelease = $releases | Where-Object { -not $_.draft -and ($_.tag_name -match '^server-v[0-9]' -or ($_.tag_name -match '^v[0-9]' -and ($_.assets | Where-Object { $_.name -match '^praimate-server-.*\.(zip|tar\.gz)$' }))) } | Select-Object -First 1
        if (-not $serverRelease) { Fail "No server release has been published yet. Use -Mode Source -Edition Server." }
        return $serverRelease.tag_name
    }
    # 1. Try GitHub REST API
    try {
        $headers = @{ "User-Agent" = "praimate-installer"; "Accept" = "application/vnd.github.v3+json" }
        if ($env:GITHUB_TOKEN) {
            $headers["Authorization"] = "token $env:GITHUB_TOKEN"
        }
        $r = Invoke-RestMethod -Uri $ReleaseApiURL -UseBasicParsing -Headers $headers -ErrorAction Stop
        if ($r.tag_name) {
            return $r.tag_name
        }
    } catch {}

    # 2. Fallback: Query Web release redirect to bypass API rate limits / 403 Forbidden
    try {
        $webUrl = "$RepoURL/releases/latest"
        $req = [System.Net.HttpWebRequest]::Create($webUrl)
        $req.AllowAutoRedirect = $false
        $req.UserAgent = "praimate-installer"
        $req.Timeout = 15000
        $resp = $req.GetResponse()
        $loc = $resp.GetResponseHeader("Location")
        $resp.Close()
        if ($loc -and $loc -match "/tag/([^/?#]+)") {
            return $Matches[1]
        }
    } catch {}

    Fail "couldn't query GitHub for the latest release. Set `$env:RELEASE_TAG to pin a version and re-run."
}

function Install-Binary {
    Step "Resolving release"
    if (-not $ReleaseTag) {
        $ReleaseTag = Resolve-LatestTag
    }
    $fname = "$AssetPrefix-$Triplet.zip"
    $url   = "$RepoURL/releases/download/$ReleaseTag/$fname"
    Info "tag:   $ReleaseTag"
    Info "asset: $fname"
    Info "url:   $url"

    Step "Downloading"
    $tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ("praimate-install-" + [Guid]::NewGuid().ToString("N")))
    try {
        $zip = Join-Path $tmp.FullName $fname
        $maxTries = 3
        for ($try = 1; $try -le $maxTries; $try++) {
            try {
                Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing `
                    -Headers @{ "User-Agent" = "praimate-installer" } -ErrorAction Stop
                break
            } catch {
                $status = $null
                if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
                if ($status -eq 404) {
                    Fail "release $ReleaseTag has no asset $fname - see $RepoURL/releases"
                }
                if ($try -eq $maxTries) {
                    Fail "download failed after $maxTries attempts: $($_.Exception.Message)"
                }
                Warn "    download attempt $try failed ($($_.Exception.Message)); retrying..."
                Start-Sleep -Seconds ($try * 2)
            }
        }
        Info ("downloaded " + ((Get-Item $zip).Length / 1MB).ToString("0.0") + " MB")

        Step "Extracting"
        Expand-Archive -Path $zip -DestinationPath $tmp.FullName -Force
        $extracted = Join-Path $tmp.FullName $Triplet
        if (-not (Test-Path $extracted)) {
            Fail "unexpected archive layout under $($tmp.FullName)"
        }

        Assert-BundleEdition $extracted
        Step "Installing to $Dest"
        Copy-Item -Path (Join-Path $extracted "praimate.exe") -Destination $Dest -Force
        Copy-Item -Path (Join-Path $extracted "wpc.exe")   -Destination $Dest -Force
        $nativeSrc = Join-Path $extracted "praimate-cli.exe"
        if (Test-Path $nativeSrc) { Copy-Item -Path $nativeSrc -Destination $Dest -Force }
        # Desktop GUI ships prebuilt in both Windows architecture archives.
        # Install it next to praimate.exe so the default launcher finds it.
        $guiSrc = Join-Path $extracted "praimate-gui.exe"
        if (-not (Test-Path $guiSrc)) {
            Fail "release bundle is missing mandatory praimate-gui.exe"
        }
        Copy-Item -Path $guiSrc -Destination $Dest -Force
        Write-Host "  v praimate-gui.exe installed (launch with: praimate)" -ForegroundColor Green
        $codeSrc = Join-Path $extracted "praimate-code.exe"
        if (Test-Path $codeSrc) {
            Copy-Item -Path $codeSrc -Destination $Dest -Force
            Write-Host "  v praimate-code.exe installed (launch with: praimate code)" -ForegroundColor Green
        }
        # Bundled standalone graphify → %APPDATA%\praimate\bin, where
        # ResolveGraphify looks first (zero-dependency RAG fallback).
        $gfSrc = Join-Path $extracted "praimate-graphify.exe"
        if (Test-Path $gfSrc) {
            $cfgbin = Join-Path $env:APPDATA "praimate\bin"
            New-Item -ItemType Directory -Force -Path $cfgbin | Out-Null
            Copy-Item -Path $gfSrc -Destination (Join-Path $cfgbin "praimate-graphify.exe") -Force
            Write-Host "  v bundled graphify installed (used for agent RAG indexing)" -ForegroundColor Green
        }
        # Ship the bundled samples next to the binary at the same path
        # the launcher probes in SampleCandidates:
        # "<execDir>\..\share\praimate\samples\workpaths". Without this,
        # the first-run "seed example templates" finds nothing.
        $samplesSrc = Join-Path $extracted "samples"
        if (Test-Path $samplesSrc) {
            $samplesDest = Join-Path (Split-Path $Dest -Parent) "share\praimate\samples"
            New-Item -ItemType Directory -Force -Path $samplesDest | Out-Null
            Copy-Item -Path (Join-Path $samplesSrc "*") -Destination $samplesDest -Recurse -Force
            Info "samples -> $samplesDest"
        }
        Write-Host "  v praimate.exe + wpc.exe installed" -ForegroundColor Green
    } finally {
        if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
    }
}

# ---------- source path: clone + go build ----------
function Have-Go { return [bool](Get-Command go -ErrorAction SilentlyContinue) }

function Install-Go {
    Step "Go isn't installed"
    if (Get-Command winget -ErrorAction SilentlyContinue) {
        Write-Host "Can install via winget:"
        Write-Host ""
        Write-Host "    winget install --id GoLang.Go -e --accept-package-agreements --accept-source-agreements"
        Write-Host ""
        if (YesNo "Run this now?") {
            & winget install --id GoLang.Go -e --accept-package-agreements --accept-source-agreements
            # winget puts go on PATH but the current shell won't see it
            # without refreshing. Pull the env var fresh.
            $env:PATH = [Environment]::GetEnvironmentVariable("PATH","Machine") + ";" + [Environment]::GetEnvironmentVariable("PATH","User")
        } else {
            Fail "Cancelled. Install Go from https://go.dev/dl/ and re-run."
        }
    } else {
        Warn "winget isn't available on this system."
        Warn "Install Go manually from https://go.dev/dl/ then re-run this script."
        exit 1
    }
    if (-not (Have-Go)) {
        Fail "Go install reported success but 'go' still isn't on PATH. Open a new PowerShell and re-run."
    }
}

function Install-Source {
    if (-not (Have-Go)) { Install-Go }
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
        Fail "git is required for --source builds. Install from https://git-scm.com/ first."
    }
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
        Fail "Node.js/npm is required for the GUI source build. Install Node.js and re-run."
    }

    Step "Cloning repo"
    $tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ("praimate-src-" + [Guid]::NewGuid().ToString("N")))
    try {
        & git clone --depth 1 --branch $SourceBranch "$RepoURL.git" (Join-Path $tmp.FullName "PrAImate")
        if ($LASTEXITCODE -ne 0) { Fail "git clone failed" }

        Step "Building"
        Push-Location (Join-Path $tmp.FullName "PrAImate")
        try {
            $env:CGO_ENABLED = "0"
            & go build -trimpath @RootBuildArgs -ldflags '-s -w' -o praimate.exe ./cmd/praimate
            if ($LASTEXITCODE -ne 0) { Fail "go build (praimate) failed" }
            & go build -trimpath @RootBuildArgs -ldflags '-s -w' -o wpc.exe   ./cmd/wpc
            if ($LASTEXITCODE -ne 0) { Fail "go build (wpc) failed" }
            & go build -trimpath @RootBuildArgs -ldflags '-s -w' -o praimate-cli.exe ./cmd/praimate-cli
            if ($LASTEXITCODE -ne 0) { Fail "go build (praimate-cli) failed" }
            Push-Location "cmd\praimate-gui\frontend"
            try {
                & npm install
                if ($LASTEXITCODE -ne 0) { Fail "npm install failed" }
                & npm run build
                if ($LASTEXITCODE -ne 0) { Fail "frontend build failed" }
            } finally { Pop-Location }
            Push-Location "cmd\praimate-gui"
            try {
                & go build -trimpath -tags $GuiBuildTags `
                    -ldflags "-s -w -H windowsgui" -o praimate-gui.exe .
                if ($LASTEXITCODE -ne 0) { Fail "go build (praimate-gui) failed" }
            } finally { Pop-Location }

            Step "Installing to $Dest"
            Copy-Item -Path ".\praimate.exe" -Destination $Dest -Force
            Copy-Item -Path ".\wpc.exe"   -Destination $Dest -Force
            Copy-Item -Path ".\praimate-cli.exe" -Destination $Dest -Force
            Copy-Item -Path ".\cmd\praimate-gui\praimate-gui.exe" -Destination $Dest -Force
            Write-Host "  v PrAImate GUI + maintenance CLI + wpc installed" -ForegroundColor Green
        } finally {
            Pop-Location
        }
    } finally {
        if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
    }
}

# ---------- local path: bins already next to us ----------
function Install-Local {
    Assert-BundleEdition $LocalBins
    Step "Installing to $Dest"
    $gui = Join-Path $LocalBins "praimate-gui.exe"
    if (-not (Test-Path $gui)) { Fail "local bundle is missing mandatory praimate-gui.exe" }
    Copy-Item -Path (Join-Path $LocalBins "praimate.exe") -Destination $Dest -Force
    Copy-Item -Path (Join-Path $LocalBins "wpc.exe")   -Destination $Dest -Force
    $nativeSrc = Join-Path $LocalBins "praimate-cli.exe"
    if (Test-Path $nativeSrc) { Copy-Item -Path $nativeSrc -Destination $Dest -Force }
    Copy-Item -Path $gui -Destination $Dest -Force
    Write-Host "  v PrAImate GUI + maintenance CLI + wpc installed" -ForegroundColor Green
}

# ---------- dispatch ----------
switch ($Mode) {
    "Binary" { Install-Binary }
    "Source" { Install-Source }
    "Local"  { Install-Local }
    default  { Fail "internal: unknown Mode $Mode" }
}

[IO.File]::WriteAllText((Join-Path $Dest "praimate-edition"), "$Edition`n", [Text.UTF8Encoding]::new($false))
Info "Installed edition: $Edition"

# ---------- PATH ----------
Step "Updating PATH"
$scope = if ($AllUsers) { "Machine" } else { "User" }
$existing = [Environment]::GetEnvironmentVariable("PATH", $scope)
if (-not $existing) { $existing = "" }
$parts = $existing -split ";" | Where-Object { $_ -ne "" }
if ($parts -notcontains $Dest) {
    $newPath = if ($existing.Trim() -eq "") { $Dest } else { "$existing;$Dest" }
    try {
        [Environment]::SetEnvironmentVariable("PATH", $newPath, $scope)
        Write-Host "  v $scope PATH now includes $Dest" -ForegroundColor Green
    } catch {
        Warn "couldn't update $scope PATH automatically: $($_.Exception.Message)"
        Warn "Add this dir to your PATH manually:"
        Write-Host "      $Dest"
    }
    # Also update the current shell so the user can test right away.
    $env:PATH = "$env:PATH;$Dest"
} else {
    Info "(already on $scope PATH)"
}


Step "Desktop shortcuts"
try {
    $ws = New-Object -ComObject WScript.Shell
    $desk = [Environment]::GetFolderPath("Desktop")
    $startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
    foreach ($dir in @($desk, $startMenu)) {
        $legacy = Join-Path $dir "PrAImate GUI.lnk"
        if (Test-Path $legacy) { Remove-Item -Force $legacy }
    }
    $targets = @(
        @{ Name = "PrAImate"; Exe = (Join-Path $Dest "praimate-gui.exe") }
    )
    foreach ($t in $targets) {
        if (Test-Path $t.Exe) {
            foreach ($dir in @($desk, $startMenu)) {
                if (Test-Path $dir) {
                    $lnk = $ws.CreateShortcut((Join-Path $dir ("{0}.lnk" -f $t.Name)))
                    $lnk.TargetPath = $t.Exe
                    $lnk.WorkingDirectory = $Dest
                    $lnk.IconLocation = "$($t.Exe),0"
                    $lnk.Save()
                }
            }
            Write-Host ("  v {0} shortcut (Desktop + Start Menu)" -f $t.Name) -ForegroundColor Green
        }
    }
} catch {
    Warn "couldn't create shortcuts: $($_.Exception.Message)"
}

Step "Done"
Write-Host "Open a new terminal, then run:    praimate"
if ($Edition -eq "server") { Write-Host "Headless server: praimate server --help (configure credentials and TLS before network access)" }
