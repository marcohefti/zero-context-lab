param(
  [Parameter(Mandatory=$true)][string]$File,
  [string]$Prefix = "$env:USERPROFILE\\.local"
)

$bin = Join-Path $Prefix "bin"
New-Item -ItemType Directory -Force -Path $bin | Out-Null
$dest = Join-Path $bin "zcl.exe"
Copy-Item -Force $File $dest
Write-Output "install: OK $dest"

