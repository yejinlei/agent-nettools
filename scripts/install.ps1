$ErrorActionPreference = "Stop"
$Repo = "yejinlei/agent-netx"
$Api  = "https://api.github.com/repos/$Repo/releases/latest"
$Base = "https://github.com/$Repo/releases/latest/download"

$Arch = "amd64"
if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { $Arch = "arm64" }

$Release = Invoke-RestMethod -Uri $Api -Headers @{ "User-Agent" = "agent-netx-install" }
$Tag = $Release.tag_name
$AssetName = "agent-netx-windows-$Arch.exe"
$Url = "$Base/$AssetName"

$InstallDir = Join-Path $env:APPDATA "agent-netx"
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
$Dst = Join-Path $InstallDir "agent-netx.exe"

Write-Host "agent-netx $Tag - downloading $AssetName ..."
$tmp = Join-Path $env:TEMP "agent-netx-$Tag.exe"
Invoke-WebRequest -Uri $Url -OutFile $tmp -UseBasicParsing
Copy-Item $tmp $Dst -Force
Write-Host "  installed to: $Dst"

$CurrentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($CurrentPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$CurrentPath", "User")
    Write-Host "  added $InstallDir to user PATH" -ForegroundColor Yellow
}
Write-Host "Done."
