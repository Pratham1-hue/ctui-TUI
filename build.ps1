# Build a small, static ctui.exe into .\dist
# Usage:  .\build.ps1            (version "dev")
#         .\build.ps1 -Version 0.1.0
param([string]$Version = "dev")

$ErrorActionPreference = "Stop"

go mod tidy
New-Item -ItemType Directory -Force -Path dist | Out-Null

$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o dist/ctui.exe .

$size = [math]::Round((Get-Item dist/ctui.exe).Length / 1MB, 1)
Write-Host "Built dist/ctui.exe ($size MB)"
