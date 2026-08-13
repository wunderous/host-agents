param(
  [string]$WslDistro = "Ubuntu-26.04",
  [string]$Artifact = "$PSScriptRoot\..\dist\host-agent-linux-x64"
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path "$PSScriptRoot\..").Path
$bootstrap = Join-Path $repoRoot "scripts\bootstrap-wsl-host-agent.sh"
if (-not (Test-Path -LiteralPath $Artifact)) {
  throw "Host-agent artifact is missing: $Artifact"
}
if (-not (Test-Path -LiteralPath $bootstrap)) {
  throw "Host-agent bootstrap script is missing: $bootstrap"
}

function Convert-ToWslPath([string]$WindowsPath) {
  $resolved = (Resolve-Path -LiteralPath $WindowsPath).Path
  if ($resolved -notmatch '^(?<drive>[A-Za-z]):(?<rest>.*)$') {
    throw "Expected a drive-qualified Windows path: $resolved"
  }
  return "/mnt/$($Matches.drive.ToLowerInvariant())$($Matches.rest -replace '\\', '/')"
}
$artifactLinux = Convert-ToWslPath $Artifact
$bootstrapLinux = Convert-ToWslPath $bootstrap
$targetUser = (& wsl.exe -d $WslDistro -- bash -lc 'id -un').Trim()
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($targetUser)) {
  throw "Unable to determine the WSL host-agent service user"
}

& wsl.exe -d $WslDistro -u root -- env `
  "OPUTE_HOST_AGENT_TARGET_USER=$targetUser" `
  "OPUTE_HOST_AGENT_BOOTSTRAP_PREPARE_PRIVILEGE=1" `
  bash $bootstrapLinux
if ($LASTEXITCODE -ne 0) { throw "Host-agent privilege-boundary preparation failed" }

& wsl.exe -d $WslDistro -- env `
  "OPUTE_HOST_AGENT_ARTIFACT=$artifactLinux" `
  bash $bootstrapLinux
if ($LASTEXITCODE -ne 0) { throw "Host-agent installation failed" }

& wsl.exe -d $WslDistro -- systemctl --user restart opute-bootstrap-mcp.service
if ($LASTEXITCODE -ne 0) { throw "Host-agent service restart failed" }

$ready = $false
for ($attempt = 0; $attempt -lt 60; $attempt++) {
  & wsl.exe -d $WslDistro -- bash -lc 'curl --fail --silent --show-error http://127.0.0.1:3014/health' *> $null
  if ($LASTEXITCODE -eq 0) { $ready = $true; break }
  Start-Sleep -Milliseconds 500
}
if (-not $ready) { throw "Host-agent MCP did not become ready on 127.0.0.1:3014" }

Write-Output "HOST_AGENT_INSTALL_PASS distro=$WslDistro artifact=$artifactLinux user=$targetUser"
