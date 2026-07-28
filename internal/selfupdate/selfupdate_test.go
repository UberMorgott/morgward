package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testSlug = "UberMorgott/morgward"

// fakeGitHub stands up a local stand-in for the GitHub releases API serving one
// release: tag, the binary for THIS GOOS/GOARCH (unless bin is nil) and a
// checksums.txt (unless sums is nil). It points apiBase at itself for the test.
func fakeGitHub(t *testing.T, tag string, bin, sums []byte) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + testSlug + "/releases/latest":
			assets := ""
			if bin != nil {
				assets += fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/bin"}`,
					AssetName(testSlug), srv.URL)
			}
			if sums != nil {
				if assets != "" {
					assets += ","
				}
				assets += fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/sums"}`,
					ChecksumsAsset, srv.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, tag, assets)
		case "/bin":
			_, _ = w.Write(bin)
		case "/sums":
			_, _ = w.Write(sums)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	saved := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = saved })
	return srv
}

// sumsFor builds a `sha256sum --text` style checksums.txt listing bin under the
// asset name this platform would download, plus an unrelated line to prove the
// parser selects by name rather than taking the first row.
func sumsFor(bin []byte) []byte {
	sum := sha256.Sum256(bin)
	return []byte("0000000000000000000000000000000000000000000000000000000000000000  someone-else\n" +
		hex.EncodeToString(sum[:]) + "  " + AssetName(testSlug) + "\n")
}

// stubExe writes a file standing in for the running executable and returns its path.
func stubExe(t *testing.T, content string) string {
	t.Helper()
	name := "morgward"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil { // #nosec G306
		t.Fatalf("write stub exe: %v", err)
	}
	return p
}

// TestAssetNameMatchesReleasePipeline pins the asset name against the Makefile /
// build.ps1 output pattern `dist/<repo>-<goos>-<goarch>[.exe]`. If the release
// pipeline is renamed and this is not, every self-update silently becomes "no
// release asset found for this OS/arch".
func TestAssetNameMatchesReleasePipeline(t *testing.T) {
	want := "morgward-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got := AssetName(testSlug); got != want {
		t.Fatalf("AssetName(%q) = %q, want %q", testSlug, got, want)
	}
	// The slug's repo half is the source; a bare name works too.
	if got := AssetName("morgward"); got != want {
		t.Fatalf("AssetName(\"morgward\") = %q, want %q", got, want)
	}
}

// TestChecksumAssetName pins the name the update path demands against the name
// the release pipeline publishes (Makefile / build.ps1 / release.yml all emit
// `dist/checksums.txt`). They must be the same string or every update fails
// closed. The gate itself — refusing an asset whose SHA-256 is absent or
// mismatched — is asserted by TestApplyRefusesTamperedAsset /
// TestApplyRefusesUnlistedAsset, which prove the refusal rather than the wiring.
func TestChecksumAssetName(t *testing.T) {
	if ChecksumsAsset != "checksums.txt" {
		t.Fatalf("ChecksumsAsset = %q, want checksums.txt", ChecksumsAsset)
	}
}

// TestOldPathIsTheSweptName pins the leftover name so cmd/morgward's launch-time
// cleanupOldBinary keeps matching what replaceExe writes. It is also the name
// go-selfupdate used, so leftovers from pre-migration builds still get swept.
func TestOldPathIsTheSweptName(t *testing.T) {
	exe := filepath.Join("C:", "tools", "morgward.exe")
	want := filepath.Join("C:", "tools", ".morgward.exe.old")
	if got := OldPath(exe); got != want {
		t.Fatalf("OldPath(%q) = %q, want %q", exe, got, want)
	}
}

// TestGreaterThan drives the anti-downgrade gate (F08). The critical rows are the
// ones that must be FALSE: equal, older, and unparsable versions must never let a
// binary replacement through.
func TestGreaterThan(t *testing.T) {
	cases := []struct {
		rel, cur string
		want     bool
	}{
		{"0.8.4", "0.8.3", true},
		{"0.9.0", "0.8.99", true},
		{"1.0.0", "0.99.99", true},
		{"0.8.3", "0.8.3", false}, // equal is not newer
		{"0.8.2", "0.8.3", false}, // strictly older
		{"0.8.3", "0.9.0", false},
		{"0.10.0", "0.9.0", true},  // numeric, not lexical
		{"0.9.0", "0.10.0", false}, // ...and the reverse
		{"v0.8.4", "v0.8.3", true}, // leading "v" tolerated on both sides
		{"0.8.4", "v0.8.3", true},
		{"0.8.4-rc1", "0.8.3", true},
		{"0.8.4-rc1", "0.8.4", false},    // a prerelease is older than its release
		{"0.8.4", "0.8.4-rc1", true},     // ...and the release is newer than it
		{"0.8.4+build7", "0.8.4", false}, // build metadata is not ordered
		{"", "0.8.3", false},             // fail closed
		{"garbage", "0.8.3", false},
		{"0.8.4", "garbage", false}, // unparsable CURRENT must not open the gate
		{"0.8", "0.8.3", false},
		{"0.8.4.1", "0.8.3", false},
		{"0.8.x", "0.8.3", false},
		{"0.8.+4", "0.8.3", false}, // strconv.Atoi would have accepted "+4"
		{"0.8.-4", "0.8.3", false},
	}
	for _, c := range cases {
		r := &Release{version: strings.TrimPrefix(c.rel, "v")}
		if got := r.GreaterThan(c.cur); got != c.want {
			t.Errorf("Release(%q).GreaterThan(%q) = %v, want %v", c.rel, c.cur, got, c.want)
		}
	}
}

// TestLatestFindsPlatformAsset is the happy path: tag parsed, "v" stripped, and
// both the platform binary and checksums.txt resolved.
func TestLatestFindsPlatformAsset(t *testing.T) {
	bin := []byte("new binary")
	fakeGitHub(t, "v0.9.1", bin, sumsFor(bin))

	rel, err := Latest(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel == nil {
		t.Fatal("Latest returned nil release for a release that has our asset")
	}
	if rel.Version() != "0.9.1" {
		t.Errorf("Version() = %q, want 0.9.1 (leading v stripped)", rel.Version())
	}
	if rel.AssetName() != AssetName(testSlug) {
		t.Errorf("AssetName() = %q, want %q", rel.AssetName(), AssetName(testSlug))
	}
}

// TestLatestNoChecksumsIsAnError is the F01 fail-closed gate at detect time: a
// release we could not verify must never be offered as an available update.
func TestLatestNoChecksumsIsAnError(t *testing.T) {
	fakeGitHub(t, "v0.9.1", []byte("new binary"), nil)

	rel, err := Latest(context.Background(), testSlug)
	if err == nil {
		t.Fatalf("Latest accepted a release with no %s (rel=%v)", ChecksumsAsset, rel)
	}
	if !strings.Contains(err.Error(), ChecksumsAsset) {
		t.Errorf("error %q does not name %s", err, ChecksumsAsset)
	}
}

// TestLatestNoAssetForPlatform: a release with no binary for this GOOS/GOARCH is
// "nothing to update to", not a failure — the TUI strip must show up-to-date
// rather than an error.
func TestLatestNoAssetForPlatform(t *testing.T) {
	fakeGitHub(t, "v0.9.1", nil, []byte("irrelevant"))

	rel, err := Latest(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel != nil {
		t.Fatalf("Latest = %+v, want nil for a release with no asset for this platform", rel)
	}
}

// TestLatestNoReleasesIsNotAnError: a 404 from /releases/latest (repo has never
// published one) is up-to-date, not an error.
func TestLatestNoReleasesIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	saved := apiBase
	apiBase = srv.URL
	defer func() { apiBase = saved }()

	rel, err := Latest(context.Background(), testSlug)
	if err != nil || rel != nil {
		t.Fatalf("Latest = (%v, %v), want (nil, nil) on 404", rel, err)
	}
}

// TestLatestRejectsNonVersionTag: a tag that is not a version would make the
// anti-downgrade gate compare against garbage, so it is refused up front.
func TestLatestRejectsNonVersionTag(t *testing.T) {
	bin := []byte("new binary")
	fakeGitHub(t, "nightly", bin, sumsFor(bin))

	if rel, err := Latest(context.Background(), testSlug); err == nil {
		t.Fatalf("Latest accepted a non-version tag (rel=%+v)", rel)
	}
}

// TestApplyReplacesBinary is the end-to-end swap: the verified download lands at
// exePath and the replaced binary is parked at OldPath for the launch-time sweep.
func TestApplyReplacesBinary(t *testing.T) {
	bin := []byte("#!/bin/sh\necho new\n")
	fakeGitHub(t, "v0.9.1", bin, sumsFor(bin))

	exe := stubExe(t, "old binary")
	rel, err := Latest(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if err := rel.Apply(context.Background(), exe); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read replaced exe: %v", err)
	}
	if string(got) != string(bin) {
		t.Fatalf("exe content = %q, want %q", got, bin)
	}
	// The ".new" scratch file must never survive a successful swap.
	dir, base := filepath.Split(exe)
	if _, err := os.Stat(filepath.Join(dir, "."+base+".new")); !os.IsNotExist(err) {
		t.Errorf(".new scratch file left behind (stat err = %v)", err)
	}
	// Off Windows the old binary is unlinked outright; on Windows the file may
	// survive (locked/hidden) and is swept by the next launch — either is fine, but
	// it must be at exactly OldPath so cleanupOldBinary can find it.
	if _, err := os.Stat(OldPath(exe)); err == nil {
		t.Logf("old binary parked at %s (swept at next launch)", OldPath(exe))
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(OldPath(exe)); !os.IsNotExist(err) {
			t.Errorf("old binary not removed on %s (stat err = %v)", runtime.GOOS, err)
		}
	}
}

// TestApplyRefusesTamperedAsset is the F01 gate at apply time — the assertion the
// old *selfupdate.ChecksumValidator wiring test stood in for, made direct: a
// download whose SHA-256 does not match checksums.txt must NOT be installed, and
// the running binary must be left untouched.
func TestApplyRefusesTamperedAsset(t *testing.T) {
	// checksums.txt lists the hash of the honest build; the server serves another.
	fakeGitHub(t, "v0.9.1", []byte("EVIL PAYLOAD"), sumsFor([]byte("honest build")))

	exe := stubExe(t, "old binary")
	rel, err := Latest(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	err = rel.Apply(context.Background(), exe)
	if err == nil {
		t.Fatal("Apply installed an asset whose checksum did not match checksums.txt")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %q, want a checksum mismatch", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("read exe: %v", err)
	}
	if string(got) != "old binary" {
		t.Fatalf("running binary was modified by a refused update: %q", got)
	}
	if _, err := os.Stat(OldPath(exe)); !os.IsNotExist(err) {
		t.Errorf("refused update still moved the running binary aside (stat err = %v)", err)
	}
}

// TestApplyRefusesUnlistedAsset: checksums.txt exists but does not list OUR asset.
// "No hash for this file" must fail closed, never fall through to installing it.
func TestApplyRefusesUnlistedAsset(t *testing.T) {
	bin := []byte("new binary")
	sum := sha256.Sum256(bin)
	other := []byte(hex.EncodeToString(sum[:]) + "  morgward-plan9-riscv\n")
	fakeGitHub(t, "v0.9.1", bin, other)

	exe := stubExe(t, "old binary")
	rel, err := Latest(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if err := rel.Apply(context.Background(), exe); err == nil {
		t.Fatal("Apply installed an asset that checksums.txt does not list")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old binary" {
		t.Fatalf("running binary was modified by a refused update: %q", got)
	}
}

// TestSumForParsesBothChecksumFormats covers `sha256sum --text` ("h  name") and
// `--binary` ("h *name"), and the not-listed error.
func TestSumForParsesBothChecksumFormats(t *testing.T) {
	const h = "abc123"
	for _, sums := range []string{
		"deadbeef  other\n" + h + "  morgward-x\n",
		"deadbeef *other\n" + h + " *morgward-x\n",
		h + "  morgward-x", // no trailing newline
	} {
		got, err := sumFor([]byte(sums), "morgward-x")
		if err != nil {
			t.Fatalf("sumFor(%q): %v", sums, err)
		}
		if got != h {
			t.Errorf("sumFor(%q) = %q, want %q", sums, got, h)
		}
	}
	if _, err := sumFor([]byte("deadbeef  other\n"), "morgward-x"); err == nil {
		t.Error("sumFor accepted a checksums.txt that does not list the asset")
	}
}

// TestRequireHTTPS proves the plaintext-asset refusal is live against the real
// apiBase (the test servers relax it, so assert it explicitly here).
func TestRequireHTTPS(t *testing.T) {
	if err := requireHTTPS("http://evil.example/morgward"); err == nil {
		t.Error("requireHTTPS accepted a plaintext URL against the production apiBase")
	}
	if err := requireHTTPS("https://github.com/x/y/releases/download/v1/z"); err != nil {
		t.Errorf("requireHTTPS rejected an https URL: %v", err)
	}
}
