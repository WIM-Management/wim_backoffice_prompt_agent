package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/adapter/claudecode"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/config"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/daemon"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/enroll"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/queue"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/redact"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/scanner"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/uploader"
)

// Version은 릴리스 빌드 시 ldflags로 주입된다:
//
//	go build -ldflags "-X main.Version=v0.2.0" ./cmd/wim-prompt-agent
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
		if err := cmdEnroll(cfg); err != nil {
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
		if err := runOnce(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "run-once:", err)
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

	store := state.New(filepath.Join(cfg.Dir, "state.json"))
	sc := scanner.New([]model.Adapter{claudecode.New()}, store, cfg.IdleCutoff)
	q := queue.New(filepath.Join(cfg.Dir, "queue"))

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
	// 3) Upload queued events; failures leave files on disk for next run.
	up := uploader.New(cfg.BaseURL, enroll.NewKeychainStore().Get, 100)
	return q.Drain(func(b []model.Event) error { return up.Send(b) })
}

// cmdEnroll runs the device enrollment flow: Google OAuth PKCE loopback to get
// an id_token, then backend enroll → prompt-agent token in the OS keychain.
func cmdEnroll(cfg config.Config) error {
	if cfg.GoogleClientID == "" {
		return fmt.Errorf(
			"OAuth client not configured — set WIM_PROMPT_GOOGLE_CLIENT_ID (and " +
				"WIM_PROMPT_GOOGLE_CLIENT_SECRET) for the desktop OAuth client")
	}
	oauth := enroll.OAuthConfig{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		HostedDomain: cfg.GoogleHostedDomain,
	}
	label, _ := os.Hostname()
	if label == "" {
		label = "unknown"
	}
	e := enroll.New(cfg.BaseURL, enroll.NewKeychainStore(), oauth.GoogleIDToken)
	return e.Run(label)
}

// cmdInstall installs the periodic daemon for the current OS.
func cmdInstall(cfg config.Config) error {
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
		fmt.Println("Installed: co.wimcorp.promptagent (launchd)")
	case "linux":
		fmt.Printf("Installing systemd --user timer (interval %ds)...\n", intervalSec)
		if err := daemon.InstallLinux(exe, intervalSec); err != nil {
			return err
		}
		fmt.Println("Installed: wim-prompt-agent.timer (systemd --user)")
	case "windows":
		fmt.Printf("Installing Task Scheduler task (interval %ds)...\n", intervalSec)
		if err := daemon.InstallWindows(exe, intervalSec); err != nil {
			return err
		}
		fmt.Println("Installed: WimPromptAgent (Task Scheduler)")
	default:
		fmt.Fprintf(os.Stderr, "Daemon install is not supported on %s.\n", runtime.GOOS)
		fmt.Fprintln(os.Stderr, "Run `wim-prompt-agent run-once` manually or via your OS task scheduler.")
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
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
		fmt.Println("Uninstalled: co.wimcorp.promptagent")
	case "linux":
		fmt.Println("Uninstalling systemd --user timer...")
		if err := daemon.UninstallLinux(); err != nil {
			return err
		}
		fmt.Println("Uninstalled: wim-prompt-agent.timer")
	case "windows":
		fmt.Println("Uninstalling Task Scheduler task...")
		if err := daemon.UninstallWindows(); err != nil {
			return err
		}
		fmt.Println("Uninstalled: WimPromptAgent")
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return nil
}

// cmdStatus prints basic agent status.
func cmdStatus(cfg config.Config) {
	fmt.Printf("wim-prompt-agent %s\n", Version)
	fmt.Printf("Dir:          %s\n", cfg.Dir)
	fmt.Printf("BaseURL:      %s\n", cfg.BaseURL)
	// client id는 기밀 아님 — 내장(릴리스)/env/미설정 진단용
	fmt.Printf("ClientID:     %s\n", clientIDStatus(cfg))
	fmt.Printf("ScanInterval: %s\n", cfg.ScanInterval)
	fmt.Printf("IdleCutoff:   %s\n", cfg.IdleCutoff)
	fmt.Printf("OS:           %s\n", runtime.GOOS)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: wim-prompt-agent <command>")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  enroll     Enroll this device with the WIM backend")
	fmt.Fprintln(os.Stderr, "  install    Install periodic daemon (launchd/systemd/Task Scheduler)")
	fmt.Fprintln(os.Stderr, "  uninstall  Remove periodic daemon")
	fmt.Fprintln(os.Stderr, "  run-once   Scan, redact, and upload prompts once")
	fmt.Fprintln(os.Stderr, "  status     Show current configuration")
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
