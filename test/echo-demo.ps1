<#
.SYNOPSIS
    Drives the MCP Router server over stdio and prints its JSON-RPC responses.

.DESCRIPTION
    A convenience wrapper for exercising the server without a full MCP host. It
    performs the required initialize handshake and then sends whatever messages
    are in the payload file, writing each line with an explicit flush and a
    short pause so the server processes every message before stdin closes.

    If the server binary does not exist yet, it is built first.

.EXAMPLE
    .\test\echo-demo.ps1
    Builds bin\mcp-router.exe if needed, then sends echo-demo.jsonl.
#>
[CmdletBinding()]
param(
    # Path to the server binary; built automatically if it does not exist.
    [string]$Exe = (Join-Path $PSScriptRoot '..\bin\mcp-router.exe'),

    # Newline-delimited JSON-RPC payload to send.
    [string]$Payload = (Join-Path $PSScriptRoot 'echo-demo.jsonl'),

    # Pause between messages, in milliseconds.
    [int]$DelayMs = 200
)

$ErrorActionPreference = 'Stop'
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')

if (-not (Test-Path $Exe)) {
    Write-Host "Building $Exe ..."
    Push-Location $repoRoot
    try {
        & go build -o $Exe ./cmd/mcp-router
        if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    } finally {
        Pop-Location
    }
}

$psi = [System.Diagnostics.ProcessStartInfo]::new()
$psi.FileName = (Resolve-Path $Exe)
$psi.Arguments = 'stdio'
$psi.WorkingDirectory = $repoRoot
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.UseShellExecute = $false

$proc = [System.Diagnostics.Process]::Start($psi)

foreach ($line in Get-Content -LiteralPath $Payload) {
    if ($line.Trim().Length -eq 0) { continue }
    $proc.StandardInput.WriteLine($line)
    $proc.StandardInput.Flush()
    Start-Sleep -Milliseconds $DelayMs
}
$proc.StandardInput.Close()

$out = $proc.StandardOutput.ReadToEnd()
$null = $proc.StandardError.ReadToEnd()
$proc.WaitForExit()

Write-Host '--- server responses (stdout) ---'
$out.Trim()
