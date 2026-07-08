package daemon

import (
	"fmt"
	"os/exec"
)

const windowsTaskName = "WimBackofficePromptAgent"

// InstallWindows registers a Windows Task Scheduler task that runs
// wim-backoffice-prompt-agent run-once every intervalSec seconds (rounded to minutes, min 1).
// 사용자 세션 태스크라 관리자 권한이 필요 없다. 콘솔 앱 특성상 실행 순간
// 창이 잠깐 떴다 사라질 수 있다(수집 자체엔 영향 없음).
func InstallWindows(execPath string, intervalSec int) error {
	minutes := intervalSec / 60
	if minutes < 1 {
		minutes = 1
	}

	// /F: 기존 태스크 덮어쓰기(재설치 멱등). 경로 공백 대비 exe를 내부 인용.
	return exec.Command("schtasks", "/Create", "/F",
		"/SC", "MINUTE", "/MO", fmt.Sprintf("%d", minutes),
		"/TN", windowsTaskName,
		"/TR", fmt.Sprintf(`"%s" run-once`, execPath),
	).Run()
}

// UninstallWindows removes the Task Scheduler task.
func UninstallWindows() error {
	return exec.Command("schtasks", "/Delete", "/F", "/TN", windowsTaskName).Run()
}
