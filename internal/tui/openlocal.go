package tui

import (
	"bytes"
	"hash/crc32"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
)

// openTempDir is the stable host directory that files opened-for-edit are downloaded into
// (created on demand). Distinct from downloadsDir so an explicit Download and an Open don't
// collide. Cleaned on workspace close (fileSession.close).
func openTempDir() string {
	return filepath.Join(os.TempDir(), "morgward-open")
}

// openLocalDest resolves a REMOTE path to its deterministic LOCAL temp path. The basename is
// kept (so the editor shows a sensible name + the right extension for syntax highlighting),
// prefixed with a short stable hash of the FULL remote path so two files with the same base
// in different remote directories never clobber each other locally. filepath.Base on the
// remote base guards against a crafted name escaping the temp dir.
func openLocalDest(remotePath string) string {
	base := path.Base(remotePath) // remote → forward-slash semantics
	h := crc32.ChecksumIEEE([]byte(remotePath))
	return filepath.Join(openTempDir(), strconv.FormatUint(uint64(h), 16)+"-"+filepath.Base(base))
}

// isBinary reports whether data looks non-text, using git's heuristic: a NUL byte in the
// inspected prefix. The caller reads only the first ~8 KiB. Empty content is treated as text.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0x00) >= 0
}

// openLocalFile launches the host's default handler for path via os/exec with discrete ARGS
// (never a shell string — the remote-derived name can't be reinterpreted by a shell). The
// per-OS command vector is built by the build-tagged osOpenArgs.
func openLocalFile(path string) error {
	name, args := osOpenArgs(path)
	return exec.Command(name, args...).Start() // #nosec G204 -- fixed opener + discrete path arg
}

// openLocalFileFn is the indirection the open-done handler calls so tests can stub the OS
// launch (which would otherwise spawn a real editor/cmd process). Production = openLocalFile.
var openLocalFileFn = openLocalFile
