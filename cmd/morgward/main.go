// Command morgward is a portable, single-binary executor for the
// VPS-PREP-RUNBOOK: it connects to a fresh Ubuntu 24.04/26.04 VPS over an
// embedded SSH client and applies the hardening + tuning sequence.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/tui"
	"github.com/UberMorgott/morgward/internal/version"
)

const usage = `morgward — portable executor for VPS-PREP-RUNBOOK

Usage:
  morgward [command] [flags]

Commands:
  run               full Phase A hardening + §V verification (default)
  detect            read-only discovery + inventory; changes nothing
  verify            run only the §V verification matrix
  step <ID...>      run only the named steps, e.g. "step A4 A5"
  tui               launch the interactive terminal UI (default with no args)
  version           print the program version and exit
  help              show this help

Step IDs: PRE A1 A8 A2 A2.5 A3 A4 A5 A6 A6.5 A6.7 A7 A9 A10

Flags:
  --host         VPS address (IP/hostname); prompts if omitted
  --port         SSH port (default 22)
  --user         bootstrap SSH user (default root)
  --password     bootstrap password (prompts if omitted and no --key)
  --key          existing private key path (skips key generation)
  --mode         soft | strict (default soft)
  --admin-user   non-root sudo user to create/verify (default vpsadmin)
  --log-file     write a full run log to this file (default: no file written)
  --assume-yes   proceed on a brownfield box without prompting

Note:
  On the password path a fresh ed25519 key is generated for SSH. The generated
  SSH key is printed to stdout and stored nowhere — save it.

Examples:
  morgward --host 1.2.3.4 --user root --password XXX --mode soft
  morgward detect --host 1.2.3.4 --user root --password XXX
  morgward verify --host 1.2.3.4 --key ./id_ed25519_1-2-3-4
  morgward step A4 A5 --host 1.2.3.4 --key ./id_ed25519_1-2-3-4
  # non-interactive (no prompts): all flags supplied + --assume-yes
  morgward run --host 1.2.3.4 --user root --password XXX --mode soft --assume-yes
`

func main() {
	// Set the console window title up front so the taskbar/title bar shows the
	// program name rather than the launch command path (Windows; no-op elsewhere).
	setConsoleTitle(version.Name + " — " + version.Tagline)

	// First non-flag arg is the command. Bare invocation (no args) launches the
	// TUI — the "open a window, type host/port/user/password" flow.
	args := os.Args[1:]
	cmd := "run"
	if len(args) == 0 {
		cmd = "tui"
	} else if !strings.HasPrefix(args[0], "-") {
		cmd = strings.ToLower(args[0])
		args = args[1:]
	} else {
		// Bare top-level flags that act as commands (e.g. "-h", "--version").
		switch strings.ToLower(args[0]) {
		case "-h", "--help":
			cmd = "help"
		case "-v", "--version":
			cmd = "version"
		}
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return
	}
	if cmd == "version" || cmd == "-v" || cmd == "--version" {
		fmt.Println(version.Name + " " + version.Version)
		return
	}
	if cmd == "tui" {
		if err := tui.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "tui error:", err)
			os.Exit(1)
		}
		return
	}

	cfg := &config.Config{}
	var modeStr string
	fs := flag.NewFlagSet("morgward", flag.ExitOnError)
	fs.Usage = func() { fmt.Print(usage) }
	bindFlags(fs, cfg, &modeStr)
	_ = fs.Parse(args)
	cfg.Mode = config.Mode(strings.ToLower(modeStr))
	stepIDs := fs.Args() // positional args after flags (step IDs)

	// Secrets via env (no leak into shell history / process args / transcripts).
	if cfg.Password == "" {
		cfg.Password = os.Getenv("VPS_PASSWORD")
	}
	if cfg.Host == "" {
		cfg.Host = os.Getenv("VPS_HOST")
	}

	// Interactive prompts when host/credentials are absent (the "open a window,
	// type host/user/password" flow), then watch the live log in the terminal.
	if cfg.Host == "" {
		if err := interactive(cfg, &modeStr); err != nil {
			fmt.Fprintln(os.Stderr, "input error:", err)
			os.Exit(2)
		}
		cfg.Mode = config.Mode(strings.ToLower(modeStr))
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(2)
	}

	banner(cfg)

	if cmd == "step" && len(stepIDs) == 0 {
		fmt.Fprintln(os.Stderr, "step: provide one or more step IDs, e.g. step A4 A5")
		os.Exit(2)
	}

	// CLI prints the ephemeral key to stdout (the TUI has its own key screen and
	// leaves OnKey nil). OnKey fires only on the password path; detect/verify do
	// not generate a key, so it simply won't fire there.
	if err := engine.Execute(cfg, cmd, stepIDs, engine.Hooks{OnKey: printKeyBlock}); err != nil {
		fmt.Fprintln(os.Stderr, "\nfailed:", err)
		os.Exit(1)
	}
}

// printKeyBlock writes the generated SSH private key PEM to stdout, fenced by a
// human/AI-readable banner. The key is stored nowhere else, so this is the only
// chance to save it. It goes to stdout directly, never the logger (which redacts
// secrets).
func printKeyBlock(pem string) {
	fmt.Println("----- MORGWARD SSH KEY (not stored anywhere — save it) -----")
	fmt.Print(pem)
	if !strings.HasSuffix(pem, "\n") {
		fmt.Println()
	}
	fmt.Println("----- END KEY -----")
}

func bindFlags(fs *flag.FlagSet, cfg *config.Config, modeStr *string) {
	fs.StringVar(&cfg.Host, "host", "", "VPS address (IP or hostname)")
	fs.IntVar(&cfg.Port, "port", 22, "SSH port")
	fs.StringVar(&cfg.User, "user", "root", "bootstrap SSH user")
	fs.StringVar(&cfg.Password, "password", "", "bootstrap password (omit to be prompted)")
	fs.StringVar(&cfg.KeyPath, "key", "", "existing private key path (skips key generation)")
	fs.StringVar(modeStr, "mode", "soft", "hardening mode: soft | strict")
	fs.StringVar(&cfg.AdminUser, "admin-user", "vpsadmin", "non-root sudo user to create/verify")
	fs.StringVar(&cfg.LogFile, "log-file", "", "write a full run log to this file (default: no file written)")
	fs.BoolVar(&cfg.Assume, "assume-yes", false, "proceed on a brownfield box without prompting")
}

func interactive(cfg *config.Config, modeStr *string) error {
	r := bufio.NewReader(os.Stdin)
	fmt.Println("=== morgward — interactive setup ===")
	cfg.Host = prompt(r, "VPS host (IP/hostname)", "")
	cfg.User = prompt(r, "SSH user", "root")
	cfg.Port = atoiDefault(prompt(r, "SSH port", "22"), 22)

	keyPath := prompt(r, "Private key path (blank = use password + generate ed25519)", "")
	cfg.KeyPath = keyPath
	if keyPath == "" {
		pw, err := promptPassword("SSH password")
		if err != nil {
			return err
		}
		cfg.Password = pw
	}

	m := strings.ToLower(prompt(r, "Hardening mode [soft/strict]", "soft"))
	if m != "soft" && m != "strict" {
		m = "soft"
	}
	*modeStr = m
	cfg.AdminUser = prompt(r, "Admin user to create", "vpsadmin")
	return nil
}

func prompt(r *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func promptPassword(label string) (string, error) {
	fmt.Printf("%s: ", label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		// Fallback for non-terminal stdin (piped input).
		r := bufio.NewReader(os.Stdin)
		line, _ := r.ReadString('\n')
		return strings.TrimSpace(line), nil
	}
	return strings.TrimSpace(string(b)), nil
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func banner(cfg *config.Config) {
	fmt.Printf("\nmorgward → %s@%s:%d  mode=%s  admin=%s\n\n",
		cfg.User, cfg.Host, cfg.Port, cfg.Mode, cfg.AdminUser)
}
