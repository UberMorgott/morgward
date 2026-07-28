// Package selfupdate replaces the running binary with the latest GitHub release,
// gated on the SHA-256 published in that release's checksums.txt.
//
// It is deliberately narrow because morgward's release pipeline is narrow: `make
// release` publishes PLAIN (UPX-packed) executables named "<repo>-<goos>-<goarch>"
// plus a `sha256sum --text` checksums.txt, to ONE public GitHub repository. So the
// whole job is one REST call, an exact asset-name match, a SHA-256 compare and the
// rename dance — no archive codecs, no GitLab/Gitea/private-repo sources, no PGP.
//
// It replaces github.com/creativeprojects/go-selfupdate, which linked 16 modules /
// 22 packages (Gitea + GitLab + GitHub SDKs, OAuth2, httpsig, xz) for features
// morgward never used, and whose unconditional `golang.org/x/crypto/openpgp` import
// (validate.go:17, unreachable here — only its init() ran) propagated GO-2026-5932
// into every build. That advisory has no fixed version, so no bump could clear it.
//
// Nothing here writes to stdout/stderr: the TUI owns the terminal, so every failure
// comes back as an error for the caller to render.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// apiBase is the GitHub REST root. A var only so tests can point it at a local
// httptest server; production never changes it.
var apiBase = "https://api.github.com"

// ChecksumsAsset is the per-release asset listing the SHA-256 of every release
// binary (one "<hash>  <filename>" line each, `sha256sum` format). A release
// without it is refused outright rather than applied unverified — the update path
// has no other authenticity gate.
const ChecksumsAsset = "checksums.txt"

// Response size caps. The JSON and checksum list are small; the binary cap is far
// above the ~17 MB unpacked build but bounds an unbounded read from a hostile or
// broken endpoint.
const (
	maxJSONSize  = 4 << 20   // 4 MiB
	maxSumsSize  = 1 << 20   // 1 MiB
	maxAssetSize = 256 << 20 // 256 MiB
)

// Release is a published release that has a binary for THIS GOOS/GOARCH.
type Release struct {
	version   string // semver without the leading "v"
	assetName string
	assetURL  string
	sumsURL   string
}

// Version is the release version string, without a leading "v" (matching what the
// previous library returned, which the TUI strip and CLI messages render).
func (r *Release) Version() string { return r.version }

// AssetName is the release binary this build would download.
func (r *Release) AssetName() string { return r.assetName }

// GreaterThan reports whether the release is strictly newer than current — the
// anti-downgrade gate (F08). Fail-closed: an unparsable version on either side
// returns false, so a garbled tag can never trigger a binary replacement. (The
// library this replaced panicked via semver.MustParse instead.)
func (r *Release) GreaterThan(current string) bool { return compareVer(r.version, current) > 0 }

// AssetName returns the release binary name for this build, derived from the
// repository half of an "owner/repo" slug. It must match the Makefile / build.ps1
// output names: `dist/<repo>-<goos>-<goarch>[.exe]`.
func AssetName(slug string) string {
	repo := slug
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		repo = slug[i+1:]
	}
	name := repo + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// OldPath is where Apply parks the binary it replaces. Exported so the launch-time
// sweep and the writer share ONE name and can't drift apart: on Windows a running
// executable cannot be deleted, only renamed aside, and the leftover stays locked
// until that process exits — so only a later launch can remove it.
func OldPath(exePath string) string {
	dir, base := filepath.Split(exePath)
	return filepath.Join(dir, "."+base+".old")
}

// Latest fetches the newest non-draft, non-prerelease release of the "owner/repo"
// slug and reports the asset for this GOOS/GOARCH.
//
// A repository with no releases, or a release carrying no binary for this
// platform, is (nil, nil) — "nothing to update to", not a failure. A release that
// HAS our binary but no checksums.txt IS an error: applying it would mean running
// an unverified download.
func Latest(ctx context.Context, slug string) (*Release, error) {
	body, code, err := httpGet(ctx, apiBase+"/repos/"+slug+"/releases/latest",
		"application/vnd.github+json", maxJSONSize)
	if err != nil {
		return nil, err
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil // no published release yet
	default:
		return nil, fmt.Errorf("github releases API: HTTP %d", code)
	}

	var gh struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, fmt.Errorf("parse release JSON: %w", err)
	}

	rel := &Release{
		version:   strings.TrimPrefix(strings.TrimSpace(gh.TagName), "v"),
		assetName: AssetName(slug),
	}
	for _, a := range gh.Assets {
		switch a.Name {
		case rel.assetName:
			rel.assetURL = a.URL
		case ChecksumsAsset:
			rel.sumsURL = a.URL
		}
	}
	if rel.assetURL == "" {
		return nil, nil // release exists, but not for this OS/arch
	}
	if rel.sumsURL == "" {
		return nil, fmt.Errorf("release %s has no %s asset — refusing an unverifiable update",
			gh.TagName, ChecksumsAsset)
	}
	if _, _, ok := parseVer(rel.version); !ok {
		return nil, fmt.Errorf("release tag %q is not a version", gh.TagName)
	}
	for _, u := range []string{rel.assetURL, rel.sumsURL} {
		if err := requireHTTPS(u); err != nil {
			return nil, err
		}
	}
	return rel, nil
}

// Apply downloads the release binary, verifies it against the SHA-256 published in
// checksums.txt, and swaps it in at exePath. A hash that is absent from or does not
// match checksums.txt aborts BEFORE anything on disk is touched (F01).
func (r *Release) Apply(ctx context.Context, exePath string) error {
	sums, code, err := httpGet(ctx, r.sumsURL, "application/octet-stream", maxSumsSize)
	if err != nil {
		return fmt.Errorf("download %s: %w", ChecksumsAsset, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", ChecksumsAsset, code)
	}
	want, err := sumFor(sums, r.assetName)
	if err != nil {
		return err
	}

	bin, code, err := httpGet(ctx, r.assetURL, "application/octet-stream", maxAssetSize)
	if err != nil {
		return fmt.Errorf("download %s: %w", r.assetName, err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", r.assetName, code)
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, %s lists %s",
			r.assetName, got, ChecksumsAsset, want)
	}
	return replaceExe(exePath, bin)
}

// replaceExe swaps data in at exePath through the rename dance that works while the
// binary is running (Windows cannot delete or overwrite a live .exe, but it CAN
// rename it): write ".<base>.new" → move the live binary to ".<base>.old" → move
// ".<base>.new" into place, rolling back if that last move fails.
func replaceExe(exePath string, data []byte) error {
	dir, base := filepath.Split(exePath)
	newPath := filepath.Join(dir, "."+base+".new")
	oldPath := OldPath(exePath)

	// Inherit the live binary's permissions so an update can't silently widen or
	// drop the executable bit; 0755 only when it can't be stat'ed.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exePath); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(newPath, data, mode); err != nil { // #nosec G306 -- an executable must be executable
		return fmt.Errorf("write new binary: %w", err)
	}
	// WriteFile only applies mode when it CREATES the file; a leftover .new from an
	// interrupted update would keep its old mode.
	if err := os.Chmod(newPath, mode); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("chmod new binary: %w", err)
	}

	// A leftover .old (previous update, still locked at the time) would block the
	// rename on Windows.
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("move running binary aside: %w", err)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // roll back — leave a working binary behind
		_ = os.Remove(newPath)
		return fmt.Errorf("install new binary: %w", err)
	}
	// Best-effort: on Windows the old binary is still mapped by the running process,
	// so it cannot be deleted until that process exits — hide it and let the next
	// launch's sweep (OldPath) remove it.
	if err := os.Remove(oldPath); err != nil {
		_ = hideFile(oldPath)
	}
	return nil
}

// sumFor finds the SHA-256 of name in `sha256sum` output ("<hash>  <name>", or
// "<hash> *<name>" in binary mode). An unlisted asset is an error: it must never
// fall through to "no hash, install anyway".
func sumFor(sums []byte, name string) (string, error) {
	for line := range strings.Lines(string(sums)) {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s does not list %s — refusing an unverifiable update", ChecksumsAsset, name)
}

// requireHTTPS refuses a plaintext asset URL. The URLs come from the API response,
// which is only as trustworthy as the TLS connection that delivered it, so a
// downgraded asset fetch would void the whole chain. Skipped when apiBase itself
// is not https — that only happens in tests pointing at a local server.
func requireHTTPS(u string) error {
	if !strings.HasPrefix(apiBase, "https://") {
		return nil
	}
	if !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("refusing non-HTTPS release asset URL %q", u)
	}
	return nil
}

// httpGet fetches url and returns the body (capped at limit) with the status code.
// Redirects are followed by the default client, which is how a GitHub
// browser_download_url resolves to objects.githubusercontent.com.
func httpGet(ctx context.Context, url, accept string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub rejects API requests without a User-Agent.
	req.Header.Set("User-Agent", "morgward-selfupdate")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(body)) == limit {
		return nil, resp.StatusCode, fmt.Errorf("response from %s exceeds %d bytes", url, limit)
	}
	return body, resp.StatusCode, nil
}

// compareVer compares two "X.Y.Z[-pre][+build]" versions (a leading "v" optional),
// returning -1/0/+1 — or 0 when either side is unparsable, which makes
// GreaterThan fail closed. Per semver a prerelease sorts BEFORE its release.
//
// ponytail: prerelease identifiers are compared as plain strings rather than by
// semver's dot-separated numeric/alpha rules — morgward has only ever tagged
// vX.Y.Z. Swap in golang.org/x/mod/semver if prerelease ordering ever matters.
func compareVer(a, b string) int {
	an, ap, aok := parseVer(a)
	bn, bp, bok := parseVer(b)
	if !aok || !bok {
		return 0
	}
	for i := range an {
		if an[i] != bn[i] {
			if an[i] > bn[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "": // release beats its own prerelease
		return 1
	case bp == "":
		return -1
	case ap > bp:
		return 1
	default:
		return -1
	}
}

// parseVer splits "vX.Y.Z-pre+build" into its numeric triple and prerelease tag.
// ok is false for anything that is not exactly three non-negative decimal fields.
func parseVer(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 { // build metadata is not ordered
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre, v = v[i+1:], v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		// strconv.Atoi would accept "+5" / "-5"; a semver field is digits only.
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return nums, "", false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}
