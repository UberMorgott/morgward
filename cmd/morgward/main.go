// Command morgward is a portable, single-binary executor for the
// VPS-PREP-RUNBOOK: it connects to an Ubuntu 24.04/26.04 VPS (fresh or already
// running services) over an embedded SSH client and applies the hardening +
// tuning sequence, coexisting with detected services on a brownfield box.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
	"golang.org/x/term"

	"github.com/UberMorgott/morgward/internal/config"
	"github.com/UberMorgott/morgward/internal/engine"
	"github.com/UberMorgott/morgward/internal/tui"
	"github.com/UberMorgott/morgward/internal/version"
)

// updateRepo is the GitHub "owner/repo" slug self-update pulls releases from.
const updateRepo = "UberMorgott/morgward"

// checksumsFile is the per-release asset listing the SHA-256 of every release
// binary (one "<hash>  <filename>" line each). Self-update refuses to apply any
// asset whose hash is absent or mismatched, so releases MUST publish a
// checksums.txt alongside the binaries (goreleaser's `checksum` block emits this
// by default). Without it, go-selfupdate's DetectLatest fails closed with
// ErrValidationAssetNotFound rather than downloading an unverified binary.
const checksumsFile = "checksums.txt"

// newUpdater builds a go-selfupdate Updater whose downloads are gated on a
// SHA-256 ChecksumValidator. With a validator set, both DetectLatest and the
// download path verify the asset against checksums.txt before it can replace the
// running binary — closing the unverified-binary RCE (F01). Shared by the CLI
// update path and the TUI launch-strip check so neither can skip verification.
func newUpdater() (*selfupdate.Updater, error) {
	return selfupdate.NewUpdater(newUpdaterConfig())
}

// newUpdaterConfig is the single source of the self-update config, split out so a
// test can assert the checksum validator is wired (Updater hides the field).
func newUpdaterConfig() selfupdate.Config {
	return selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: checksumsFile},
	}
}

const usage = `morgward — portable executor for VPS-PREP-RUNBOOK

Usage:
  morgward [command] [flags]

Commands:
  run               full Phase A hardening + §V verification (default)
  detect            read-only discovery + inventory; changes nothing
  verify            run only the §V verification matrix
  audit             read-only audit (server facts + tweak audit); changes nothing
  step <ID...>      run only the named steps, e.g. "step A4 A5"
  revert <ID...>    revert the named steps, e.g. "revert A2"
  tui               launch the interactive terminal UI (default with no args)
  update            self-update to the latest GitHub release (checksum-verified)
  version           print the program version and exit
  help              show this help

Step IDs: PRE A1 A8 A2 A2.5 A3 A4 A5 A6 A6.5 A6.7 A7 A9 A10

Flags:
  --host         VPS address (IP/hostname); prompts if omitted
  --port         SSH port (default 22)
  --user         bootstrap SSH user (default root)
  --password     bootstrap password (prompts if omitted and no --key)
  --key          existing private key path (skips key generation)
  --mode         soft (crypto only, preserves access) | strict (access lockdown), default soft
  --admin-user   non-root sudo user to create/verify (default vpsadmin)
  --log-file     write a full run log to this file (default: no file written)
  --assume-yes   proceed on a brownfield box (applies in coexistence mode)

Note:
  On the password path a fresh ed25519 key is generated for SSH. The generated
  SSH key is printed to stdout and stored nowhere — save it.
  On a non-fresh box, --assume-yes runs in COEXISTENCE mode: detected service
  ports, forwarding/routing, and disk swap are preserved. See /root/vps-inventory.md.

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
		result, err := tui.Run()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tui error:", err)
			os.Exit(1)
		}
		// The operator may have asked to self-update from the landing strip. Do it
		// AFTER Run() returns so the alt-screen has fully torn down, then relaunch.
		if result.DoUpdate {
			if err := performUpdate(result.TargetVer); err != nil {
				fmt.Fprintln(os.Stderr, "update failed:", err)
				os.Exit(1)
			}
		}
		return
	}
	if cmd == "update" {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		newVer, err := applyUpdate(ctx, "")
		if err != nil {
			// Being already up-to-date is NOT a failure: the anti-downgrade gate returns
			// an error when latest is not newer. Detect that case and report it calmly.
			if strings.Contains(err.Error(), "not newer") {
				fmt.Printf("%s v%s — уже последняя версия.\n", version.Name, version.Version)
				return
			}
			fmt.Fprintln(os.Stderr, "update failed:", err)
			os.Exit(1)
		}
		fmt.Printf("%s: обновлено до v%s. Перезапустите morgward.\n", version.Name, newVer)
		return
	}

	cfg := &config.Config{}
	var modeStr string
	fs := flag.NewFlagSet("morgward", flag.ExitOnError)
	fs.Usage = func() { fmt.Print(usage) }
	bindFlags(fs, cfg, &modeStr)
	// Go's flag package stops at the first non-flag arg, so flags placed after
	// positional step IDs (e.g. `step A4 A6.5 --host X`) would never be parsed.
	// Partition args into flags and positionals first so flag order is irrelevant.
	flagArgs, stepIDs := partitionArgs(args)
	_ = fs.Parse(flagArgs)
	cfg.Mode = config.Mode(strings.ToLower(modeStr))

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
	if err := engine.Execute(context.Background(), cfg, cmd, stepIDs, engine.Hooks{OnKey: printKeyBlock}); err != nil {
		fmt.Fprintln(os.Stderr, "\nfailed:", err)
		os.Exit(1)
	}
}

// applyUpdate downloads + verifies + replaces the running binary with the latest
// release (checksum-validated, anti-downgrade gated). Returns the new version on
// success. It does NOT relaunch — callers decide (the TUI relaunches; the CLI
// `update` command just reports and exits).
func applyUpdate(ctx context.Context, targetVer string) (string, error) {
	updater, err := newUpdater()
	if err != nil {
		return "", fmt.Errorf("new updater: %w", err)
	}
	if targetVer != "" {
		fmt.Printf("%s: обновление до v%s…\n", version.Name, targetVer)
	} else {
		fmt.Printf("%s: обновление…\n", version.Name)
	}

	// Detect first so we can vet the release BEFORE applying it. DetectLatest also
	// resolves (and requires) the checksums.txt validation asset; a release without
	// it fails closed here rather than downloading an unverified binary.
	rel, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(updateRepo))
	if err != nil {
		return "", fmt.Errorf("detect latest: %w", err)
	}
	if !found || rel == nil {
		return "", fmt.Errorf("no release asset found for this OS/arch")
	}
	// Anti-downgrade (F08): go-selfupdate's UpdateCommand only gates on version
	// inequality, so it would happily apply an OLDER "latest". Refuse anything that
	// is not strictly newer than the running build before touching the binary.
	if !rel.GreaterThan(version.Version) {
		return "", fmt.Errorf("latest release v%s is not newer than current v%s — refusing",
			rel.Version(), version.Version)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	// UpdateTo applies exactly the release we just vetted (verifying its checksum),
	// avoiding a re-detect TOCTOU window between the version gate and the download.
	if err := updater.UpdateTo(ctx, rel, exe); err != nil {
		return "", fmt.Errorf("update self: %w", err)
	}
	return rel.Version(), nil
}

// performUpdate downloads + replaces the running binary with the latest release
// via applyUpdate, then relaunches the updated executable and exits. On Windows
// the running exe cannot be deleted, so go-selfupdate renames it to "<exe>.old";
// the TUI's Init() cleans that leftover on the next launch. targetVer is the
// version the operator saw on the strip (informational; applyUpdate re-detects).
func performUpdate(targetVer string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	newVer, err := applyUpdate(ctx, targetVer)
	if err != nil {
		return err
	}

	// Locate the (now-replaced) executable to relaunch the updated binary.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	fmt.Printf("%s: обновлено до v%s — перезапуск.\n", version.Name, newVer)
	c := exec.Command(exe, os.Args[1:]...) // #nosec G204 G702 -- args are the local operator's own launch argv; no shell; binary path from os.Executable(); update authenticity is enforced by the F01 checksum validator
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		// Propagate the relaunched process's exit code when it failed cleanly.
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return fmt.Errorf("relaunch: %w", err)
	}
	os.Exit(0)
	return nil
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

// valueFlags lists the value-taking flag names bound in bindFlags (everything
// except the sole bool flag --assume-yes).
var valueFlags = map[string]bool{
	"host":       true,
	"port":       true,
	"user":       true,
	"password":   true,
	"key":        true,
	"mode":       true,
	"admin-user": true,
	"log-file":   true,
}

// partitionArgs splits args into flag tokens (and their values) and positional
// tokens (step IDs), independent of order. A value-taking flag in space form
// (`--host X`) consumes the following token as its value; `--name=value` and the
// bool `--assume-yes` consume no separate token.
func partitionArgs(args []string) (flagArgs, positional []string) {
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if len(tok) > 1 && tok[0] == '-' {
			name := strings.TrimLeft(tok, "-")
			if strings.IndexByte(name, '=') >= 0 {
				// --name=value form: self-contained flag token.
				flagArgs = append(flagArgs, tok)
				continue
			}
			flagArgs = append(flagArgs, tok)
			if valueFlags[name] && i+1 < len(args) {
				// Space form: next token is this flag's value.
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, tok)
	}
	return flagArgs, positional
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
