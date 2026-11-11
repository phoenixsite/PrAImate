$ErrorActionPreference = 'Stop'
$source = Get-Content -Raw (Join-Path $PSScriptRoot 'install.ps1')
$tokens = $null; $parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$parseErrors) > $null
if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
# Exercise the installer's actual selection block without installing binaries,
# accessing the registry, invoking a downloaded executable or requesting admin.
$start = $source.IndexOf('$EditionMarker = Join-Path $Dest')
$end = $source.IndexOf('if (-not (Test-Path $Dest))', $start)
if ($start -lt 0 -or $end -lt 0) { throw 'Edition selection block was not found' }
$select = [scriptblock]::Create('param($Dest, $Edition = "Desktop")' + "`n" + $source.Substring($start, $end-$start) + "`n" + '@{edition=$Edition;prefix=$AssetPrefix;branch=$SourceBranch;tags=$GuiBuildTags}')
$directory = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
$oldTag = $env:RELEASE_TAG
try {
    New-Item -ItemType Directory $directory > $null
    $env:RELEASE_TAG = ''
    $new = & $select -Dest $directory
    if ($new.edition -ne 'desktop' -or $new.prefix -ne 'praimate') { throw 'New installation did not default to Desktop' }
    Set-Content (Join-Path $directory 'praimate-edition') 'server'
    $existing = & $select -Dest $directory
    if ($existing.edition -ne 'server' -or $existing.prefix -ne 'praimate-server' -or $existing.tags -ne 'desktop,production,server') { throw 'Existing Server installation changed edition' }
    $explicit = & $select -Dest $directory -Edition Desktop
    if ($explicit.edition -ne 'desktop' -or $explicit.branch -ne 'main') { throw 'Explicit edition selection was ignored' }
    Set-Content (Join-Path $directory 'praimate-edition') 'invalid'
    $rejected = $false
    try { & $select -Dest $directory > $null } catch { $rejected = $true }
    if (-not $rejected) { throw 'Accepted an invalid installation identity' }
    Write-Output 'PASS: new Desktop, retained Server, explicit selection and invalid-marker rejection'
} finally {
    $env:RELEASE_TAG = $oldTag
    Remove-Item -Recurse -Force $directory
}
