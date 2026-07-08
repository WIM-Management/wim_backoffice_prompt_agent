# wim-prompt-agent 설치 스크립트 (Windows)
#
# 원라이너 (gh CLI 로그인 필요 — private repo):
#   gh api repos/WIM-Management/wim_backoffice_prompt_agent/contents/scripts/install.ps1 `
#     -H "Accept: application/vnd.github.raw" | Out-String | Invoke-Expression
#
# 하는 일: 최신 릴리스 exe 다운로드 → SHA256 검증 → %LOCALAPPDATA%\wim-prompt-agent 설치 +
# 사용자 PATH 등록 → enroll → install(작업 스케줄러).
# 옵션: $env:WIM_PROMPT_NO_SETUP=1 (바이너리 설치까지만)
$ErrorActionPreference = "Stop"

$repo = "WIM-Management/wim_backoffice_prompt_agent"
$asset = "wim-prompt-agent-windows-amd64.exe"

# --- 인증 확인 (private repo → gh 필수) ---
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    throw "gh CLI가 필요합니다. 설치: winget install GitHub.cli → gh auth login"
}
# gh auth status는 비활성 계정이 낡아도 exit 1 → 활성 토큰 존재로 판정
gh auth token *> $null
if ($LASTEXITCODE -ne 0) { throw "gh 로그인이 필요합니다: gh auth login" }

# --- 다운로드 + 체크섬 검증 ---
$dir = Join-Path $env:LOCALAPPDATA "wim-prompt-agent"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Write-Host "최신 릴리스 다운로드 중... ($asset)"
gh release download -R $repo -p $asset -O (Join-Path $dir "wim-prompt-agent.exe") --clobber
if ($LASTEXITCODE -ne 0) { throw "다운로드 실패" }
gh release download -R $repo -p SHA256SUMS -O (Join-Path $dir "SHA256SUMS") --clobber
if ($LASTEXITCODE -ne 0) { throw "SHA256SUMS 다운로드 실패" }

$expected = ((Select-String -Path (Join-Path $dir "SHA256SUMS") -Pattern ([regex]::Escape($asset))).Line -split "\s+")[0]
$actual = (Get-FileHash (Join-Path $dir "wim-prompt-agent.exe") -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "체크섬 불일치: expected=$expected actual=$actual" }
Write-Host "체크섬 OK"

# --- 사용자 PATH 등록 ---
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -notlike "*$dir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$userPath;$dir", "User")
    $env:PATH = "$env:PATH;$dir"
    Write-Host "PATH에 추가됨: $dir (새 터미널부터 wim-prompt-agent 명령 사용 가능)"
}

$exe = Join-Path $dir "wim-prompt-agent.exe"
Write-Host "설치됨: $exe"
& $exe status | Select-Object -First 1

# --- enroll + 작업 스케줄러 등록 ---
if ($env:WIM_PROMPT_NO_SETUP -eq "1") {
    Write-Host "(NO_SETUP) 다음 단계: wim-prompt-agent enroll ; wim-prompt-agent install"
    return
}
Write-Host ""
Write-Host "기기 등록을 시작합니다 — 브라우저가 열리면 회사 Google 계정으로 로그인하세요."
& $exe enroll
if ($LASTEXITCODE -ne 0) { throw "enroll 실패" }
& $exe install
if ($LASTEXITCODE -ne 0) { throw "install 실패" }
Write-Host ""
Write-Host "✅ 완료! 15분 주기로 자동 수집됩니다. 확인: wim-prompt-agent status"
