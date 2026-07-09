package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/adapter/claudecode"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/config"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/daemon"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/enroll"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/queue"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/redact"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/registry"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/scanner"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/updater"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/uploader"
)

// Version은 릴리스 빌드 시 ldflags로 주입된다:
//
//	go build -ldflags "-X main.Version=v0.2.0" ./cmd/wim-backoffice-prompt-agent
//
// 주입 없이 빌드하면 dev로 남아 로컬 빌드임을 드러낸다.
var Version = "dev"

func main() {
	cfg := config.Default()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "enroll":
		if err := cmdEnrollDispatch(cfg, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enroll:", err)
			os.Exit(1)
		}

	case "install":
		if err := cmdInstall(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "install:", err)
			os.Exit(1)
		}

	case "uninstall":
		if err := cmdUninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall:", err)
			os.Exit(1)
		}

	case "run-once":
		err := runOnce(cfg)
		// 부분 실패(한 폴더 토큰 만료 등)여도 self-update는 진행 — 한 폴더 실패가
		// 나머지 폴더의 업데이트 수신까지 영구히 막지 않게 한다.
		maybeSelfUpdate(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run-once:", err)
			os.Exit(1)
		}

	case "update":
		if err := cmdUpdate(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "update:", err)
			os.Exit(1)
		}

	case "status":
		cmdStatus(cfg)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// runOnce executes one full scan→redact→enqueue→commit→drain(upload) cycle.
// Disk persistence (Enqueue) happens before offset advance (commit) to guarantee
// zero event loss on crash (§4.5).
func runOnce(cfg config.Config) error {
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	entries, err := registry.New(registryPath(cfg)).List()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	store := state.New(filepath.Join(cfg.Dir, "state.json"))

	// 폴더별 에러 격리: 한 폴더 실패가 다른 폴더 수집을 막지 않는다.
	var failed []string
	for _, e := range entries {
		if err := collectDir(cfg, store, e); err != nil {
			fmt.Fprintf(os.Stderr, "수집 실패 [%s]: %v\n", e.ConfigDir, err)
			failed = append(failed, e.ConfigDir)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d개 폴더 수집 실패: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// collectDir runs one scan→redact→enqueue→commit→drain cycle for a single
// registered config dir, uploading with THAT dir's token. Disk persistence
// (Enqueue) precedes offset advance (commit) to guarantee zero loss on crash (§4.5).
func collectDir(cfg config.Config, store *state.Store, e registry.Entry) error {
	sc := scanner.New([]model.Adapter{claudecode.New(e.ConfigDir)}, store, cfg.IdleCutoff)
	q := queue.New(queueDirFor(cfg, e))

	evs, commit := sc.ScanOnce()
	for i := range evs {
		evs[i].PromptText = redact.Scrub(evs[i].PromptText)
		evs[i].ResponseText = redact.Scrub(evs[i].ResponseText)
		evs[i].ClientVersion = Version
	}
	// 1) Persist to disk first — no upload loss on crash.
	if err := q.Enqueue(evs); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	// 2) Advance scanner offset only after events are safely on disk.
	if err := commit(); err != nil {
		return fmt.Errorf("commit offset: %w", err)
	}
	// 3) Upload queued events with this dir's token; failures leave files for next run.
	up := uploader.New(cfg.BaseURL, enroll.NewKeychainStore(e.TokenKey).Get, 100)
	return q.Drain(func(b []model.Event) error { return up.Send(b) })
}

func registryPath(cfg config.Config) string { return filepath.Join(cfg.Dir, "registry.json") }

// queueDirFor keeps the default dir on the legacy flat queue path (cfg.Dir/queue)
// so existing queued files keep draining, and puts non-default dirs in per-slug
// subdirs. A flat glob never matches the subdirs, so per-dir tokens never cross.
func queueDirFor(cfg config.Config, e registry.Entry) string {
	if e.TokenKey == registry.DefaultTokenKey {
		return filepath.Join(cfg.Dir, "queue")
	}
	return filepath.Join(cfg.Dir, "queue", registry.Slug(e.ConfigDir))
}

// resolveConfigDir turns a --config-dir value into an absolute path: absolute
// stays as-is, otherwise resolved under home (".claude-melle" -> ~/.claude-melle).
// Empty -> the default ~/.claude.
func resolveConfigDir(v string) string {
	if v == "" {
		return registry.DefaultConfigDir()
	}
	if filepath.IsAbs(v) {
		return filepath.Clean(v)
	}
	home, _ := os.UserHomeDir()
	return filepath.Clean(filepath.Join(home, v))
}

const updateCheckInterval = 24 * time.Hour

// shouldCheckUpdate reports whether enough time passed since the last check.
func shouldCheckUpdate(last, now time.Time) bool {
	return now.Sub(last) >= updateCheckInterval
}

// maybeSelfUpdate is the silent auto-update path called after a successful
// run-once. It checks at most once per updateCheckInterval and never fails the
// caller — any error is logged and swallowed so collection is unaffected.
func maybeSelfUpdate(cfg config.Config) {
	store := state.New(filepath.Join(cfg.Dir, "state.json"))
	d, err := store.Load()
	if err != nil {
		return
	}
	if !shouldCheckUpdate(d.LastUpdateCheck, time.Now()) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	res, err := updater.CheckAndUpdate(Version, exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "self-update 확인 실패(무시): %v\n", err)
		return // state 미갱신 → 다음 run-once에서 재시도
	}
	if res.Updated {
		fmt.Fprintf(os.Stderr, "self-update: %s → %s (다음 실행부터 적용)\n", res.From, res.To)
	}
	d.LastUpdateCheck = time.Now()
	_ = store.Save(d)
}

// cmdUpdate is the manual `update` command: check now (no interval gate), print.
func cmdUpdate(cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	res, err := updater.CheckAndUpdate(Version, exe)
	if err != nil {
		return err
	}
	if res.Updated {
		fmt.Printf("업데이트 완료: %s → %s (다음 실행부터 적용)\n", res.From, res.To)
	} else if Version == "dev" {
		fmt.Println("로컬(dev) 빌드는 self-update를 건너뜁니다.")
	} else {
		fmt.Printf("이미 최신입니다 (%s)\n", Version)
	}
	return nil
}

// cmdEnrollDispatch parses `enroll` flags and routes to enroll or forget:
//
//	enroll [--config-dir <path>]           config 폴더 등록(기본 ~/.claude)
//	enroll --forget --config-dir <path>    폴더 등록해제 + 토큰 삭제
func cmdEnrollDispatch(cfg config.Config, argv []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	dir := fs.String("config-dir", "", "Claude 설정 폴더(절대경로 또는 ~ 기준 이름, 기본 ~/.claude)")
	forget := fs.Bool("forget", false, "해당 폴더 등록해제 + 토큰 삭제")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	configDir := resolveConfigDir(*dir)
	if *forget {
		return cmdForget(cfg, configDir)
	}
	return cmdEnroll(cfg, configDir)
}

// cmdEnroll runs the device enrollment flow for one config dir: register it,
// Google OAuth PKCE loopback → id_token → backend enroll → token stored under
// the dir's token key. 폴더마다 다른 사람이 로그인하면 폴더별 토큰이 분리된다.
func cmdEnroll(cfg config.Config, configDir string) error {
	if cfg.GoogleClientID == "" {
		return fmt.Errorf(
			"OAuth client not configured — set WIM_PROMPT_GOOGLE_CLIENT_ID (and " +
				"WIM_PROMPT_GOOGLE_CLIENT_SECRET) for the desktop OAuth client")
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return err
	}
	// 레지스트리 upsert 먼저 — slug 충돌 등을 OAuth 로그인 전에 걸러낸다.
	entry, err := registry.New(registryPath(cfg)).Upsert(configDir)
	if err != nil {
		return err
	}
	oauth := enroll.OAuthConfig{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		HostedDomain: cfg.GoogleHostedDomain,
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	label := host
	if !registry.IsDefault(configDir) {
		label = host + ":" + registry.Slug(configDir) // 백엔드에서 어느 폴더인지 식별
	}
	e := enroll.New(cfg.BaseURL, enroll.NewKeychainStore(entry.TokenKey), oauth.GoogleIDToken)
	if err := e.Run(label); err != nil {
		return err
	}
	fmt.Printf("✅ enroll 완료: %s (token=%s)\n", configDir, entry.TokenKey)
	return nil
}

// cmdForget de-registers a config dir and deletes its token (직원 이탈 대비).
func cmdForget(cfg config.Config, configDir string) error {
	removed, ok, err := registry.New(registryPath(cfg)).Remove(configDir)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Printf("등록되지 않은 폴더입니다: %s\n", configDir)
		return nil
	}
	if err := enroll.NewKeychainStore(removed.TokenKey).Delete(); err != nil {
		fmt.Fprintf(os.Stderr, "토큰 삭제 경고: %v\n", err)
	}
	if err := os.RemoveAll(queueDirFor(cfg, removed)); err != nil {
		// 잔여 큐를 못 지우면 그 이벤트는 다시 드레인되지 않으니(폴더 등록해제됨) 알린다.
		fmt.Fprintf(os.Stderr, "잔여 큐 정리 경고(%s): %v\n", queueDirFor(cfg, removed), err)
	}
	fmt.Printf("🗑  forget 완료: %s (token=%s 삭제)\n", configDir, removed.TokenKey)
	return nil
}

// needsEnroll is the pure decision behind ensureEnrolled (unit-tested).
// No token → enroll. Token present → re-enroll ONLY if the verifier explicitly
// rejects it; a transient/offline failure (TokenUnknown) keeps the healthy token.
// verify is lazy so an empty token never triggers a network call.
func needsEnroll(token string, verify func() enroll.TokenValidity) bool {
	if token == "" {
		return true
	}
	return verify() == enroll.TokenRejected
}

// ensureEnrolled makes sure a usable device token exists for the default
// ~/.claude before installing the daemon. 추가 폴더는 설치 후 `enroll --config-dir`로.
func ensureEnrolled(cfg config.Config) error {
	token, err := enroll.NewKeychainStore(registry.DefaultTokenKey).Get()
	if err != nil {
		fmt.Fprintf(os.Stderr, "기기 토큰 조회 실패, 재등록 진행: %v\n", err)
	}
	if needsEnroll(token, func() enroll.TokenValidity { return enroll.VerifyToken(cfg.BaseURL, token) }) {
		if token != "" {
			fmt.Println("기존 기기 등록이 만료·폐기되어 재등록합니다.")
		}
		return cmdEnroll(cfg, registry.DefaultConfigDir())
	}
	return nil
}

// cmdInstall ensures enrollment, installs the periodic daemon, then runs one
// immediate collection so the first upload happens now (not up to an interval later).
func cmdInstall(cfg config.Config) error {
	if err := ensureEnrolled(cfg); err != nil {
		return fmt.Errorf("enroll: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	intervalSec := int(cfg.ScanInterval.Seconds())

	switch runtime.GOOS {
	case "darwin":
		fmt.Printf("Installing launchd agent (interval %ds)...\n", intervalSec)
		if err := daemon.InstallMac(exe, intervalSec); err != nil {
			return err
		}
		fmt.Println("Installed: kr.co.wimcorp.wim-backoffice-prompt-agent (launchd)")
	case "linux":
		fmt.Printf("Installing systemd --user timer (interval %ds)...\n", intervalSec)
		if err := daemon.InstallLinux(exe, intervalSec); err != nil {
			return err
		}
		fmt.Println("Installed: wim-backoffice-prompt-agent.timer (systemd --user)")
	case "windows":
		fmt.Printf("Installing Task Scheduler task (interval %ds)...\n", intervalSec)
		if err := daemon.InstallWindows(exe, intervalSec); err != nil {
			return err
		}
		fmt.Println("Installed: WimBackofficePromptAgent (Task Scheduler)")
	default:
		fmt.Fprintf(os.Stderr, "Daemon install is not supported on %s.\n", runtime.GOOS)
		fmt.Fprintln(os.Stderr, "Run `wim-backoffice-prompt-agent run-once` manually or via your OS task scheduler.")
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	// 즉시 첫 수집 — 실패해도 데몬이 다음 주기에 재시도하므로 경고만.
	fmt.Println("첫 수집을 실행합니다...")
	if err := runOnce(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "첫 수집 실패(무시 — 다음 주기에 재시도): %v\n", err)
	}
	fmt.Println("✅ 설치 완료. 15분 주기로 자동 수집됩니다.")
	return nil
}

// cmdUninstall removes the periodic daemon for the current OS.
func cmdUninstall() error {
	switch runtime.GOOS {
	case "darwin":
		fmt.Println("Uninstalling launchd agent...")
		if err := daemon.UninstallMac(); err != nil {
			return err
		}
		fmt.Println("Uninstalled: kr.co.wimcorp.wim-backoffice-prompt-agent")
	case "linux":
		fmt.Println("Uninstalling systemd --user timer...")
		if err := daemon.UninstallLinux(); err != nil {
			return err
		}
		fmt.Println("Uninstalled: wim-backoffice-prompt-agent.timer")
	case "windows":
		fmt.Println("Uninstalling Task Scheduler task...")
		if err := daemon.UninstallWindows(); err != nil {
			return err
		}
		fmt.Println("Uninstalled: WimBackofficePromptAgent")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return nil
}

// cmdStatus prints basic agent status.
func cmdStatus(cfg config.Config) {
	fmt.Printf("wim-backoffice-prompt-agent %s\n", Version)
	fmt.Printf("Dir:          %s\n", cfg.Dir)
	fmt.Printf("BaseURL:      %s\n", cfg.BaseURL)
	// client id는 기밀 아님 — 내장(릴리스)/env/미설정 진단용
	fmt.Printf("ClientID:     %s\n", clientIDStatus(cfg))
	fmt.Printf("ScanInterval: %s\n", cfg.ScanInterval)
	fmt.Printf("IdleCutoff:   %s\n", cfg.IdleCutoff)
	fmt.Printf("OS:           %s\n", runtime.GOOS)

	// 등록된 config 폴더 + 각 토큰 유무
	fmt.Println("Config dirs:")
	entries, err := registry.New(registryPath(cfg)).List()
	if err != nil {
		fmt.Printf("  (레지스트리 조회 실패: %v)\n", err)
		return
	}
	for _, e := range entries {
		tok, _ := enroll.NewKeychainStore(e.TokenKey).Get()
		mark := "no token"
		if tok != "" {
			mark = "enrolled"
		}
		fmt.Printf("  %-40s [%s] %s\n", e.ConfigDir, e.TokenKey, mark)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: wim-backoffice-prompt-agent <command>")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  enroll [--config-dir <path>]           Enroll a Claude config dir (default ~/.claude)")
	fmt.Fprintln(os.Stderr, "  enroll --forget --config-dir <path>    De-register a config dir + delete its token")
	fmt.Fprintln(os.Stderr, "  install    Install periodic daemon (launchd/systemd/Task Scheduler)")
	fmt.Fprintln(os.Stderr, "  uninstall  Remove periodic daemon")
	fmt.Fprintln(os.Stderr, "  run-once   Scan, redact, and upload prompts once (all enrolled dirs)")
	fmt.Fprintln(os.Stderr, "  update     Check for and install the latest release")
	fmt.Fprintln(os.Stderr, "  status     Show current configuration + enrolled dirs")
}

// clientIDStatus describes where the OAuth client id came from (diagnostics for enroll support).
func clientIDStatus(cfg config.Config) string {
	if cfg.GoogleClientID == "" {
		return "(missing — env 또는 릴리스 바이너리 필요)"
	}
	if os.Getenv("WIM_PROMPT_GOOGLE_CLIENT_ID") != "" {
		return cfg.GoogleClientID + " (env)"
	}
	return cfg.GoogleClientID + " (embedded)"
}
