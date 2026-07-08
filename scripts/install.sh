#!/usr/bin/env bash
# wim-prompt-agent 설치 스크립트 (macOS / Linux)
#
# 원라이너 (gh CLI 로그인 필요 — private repo):
#   gh api repos/WIM-Management/wim_backoffice_prompt_agent/contents/scripts/install.sh \
#     -H "Accept: application/vnd.github.raw" | bash
#
# 하는 일: 최신 릴리스 바이너리 다운로드 → SHA256 검증 → PATH에 설치 → enroll → install(데몬).
# 옵션: --no-setup (바이너리 설치까지만, enroll/데몬 등록 생략)
set -euo pipefail

REPO="WIM-Management/wim_backoffice_prompt_agent"
BIN="wim-prompt-agent"
NO_SETUP=0
[ "${1:-}" = "--no-setup" ] && NO_SETUP=1

# --- 플랫폼 판별 ---
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in darwin|linux) ;; *) echo "지원하지 않는 OS: $os" >&2; exit 1;; esac
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "지원하지 않는 아키텍처: $arch" >&2; exit 1 ;;
esac
asset="${BIN}-${os}-${arch}"

# --- 인증 확인 (private repo → gh 필수) ---
if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI가 필요합니다. 설치: brew install gh (mac) / https://cli.github.com (linux)" >&2
  echo "설치 후: gh auth login" >&2
  exit 1
fi
# gh auth status는 비활성 계정이 낡아도 exit 1 → 활성 토큰 존재로 판정
if ! gh auth token >/dev/null 2>&1; then
  echo "gh 로그인이 필요합니다: gh auth login" >&2
  exit 1
fi

# --- 다운로드 + 체크섬 검증 ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "최신 릴리스 다운로드 중... ($asset)"
gh release download -R "$REPO" -p "$asset" -p SHA256SUMS -D "$tmp" --clobber

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && grep "  ${asset}\$" SHA256SUMS | sha256sum -c - >/dev/null)
else
  (cd "$tmp" && grep "  ${asset}\$" SHA256SUMS | shasum -a 256 -c - >/dev/null)
fi
echo "체크섬 OK"

# --- 설치 위치: /usr/local/bin 쓰기 가능하면 거기, 아니면 ~/.local/bin ---
dest="/usr/local/bin"
if [ ! -w "$dest" ]; then
  dest="$HOME/.local/bin"
  mkdir -p "$dest"
fi
install -m 0755 "$tmp/$asset" "$dest/$BIN"
[ "$os" = darwin ] && xattr -d com.apple.quarantine "$dest/$BIN" 2>/dev/null || true
echo "설치됨: $dest/$BIN ($("$dest/$BIN" status | head -1))"

case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "⚠️  $dest 가 PATH에 없습니다. 셸 프로파일에 추가하세요: export PATH=\"$dest:\$PATH\"" ;;
esac

# --- enroll + 데몬 등록 ---
if [ "$NO_SETUP" = 1 ]; then
  echo "(--no-setup) 다음 단계: $BIN enroll && $BIN install"
  exit 0
fi
echo ""
echo "기기 등록을 시작합니다 — 브라우저가 열리면 회사 Google 계정으로 로그인하세요."
"$dest/$BIN" enroll
"$dest/$BIN" install
echo ""
echo "✅ 완료! 15분 주기로 자동 수집됩니다. 확인: $BIN status"
