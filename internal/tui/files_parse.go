package tui

import (
	"strconv"
	"strings"
)

// fileEntry is one row of a remote directory listing, parsed from the stdout of
// `LC_ALL=C ls -la --time-style=long-iso`.
type fileEntry struct {
	name    string
	isDir   bool
	isLink  bool
	target  string // symlink target, "" if not a link
	size    int64
	mode    string // perms minus the type char, e.g. "rwxr-xr-x"
	modeRaw string // full first column, e.g. "drwxr-xr-x"
	mtime   string // "2026-06-09 09:20" from long-iso (date + " " + time)
}

// parseListing parses the stdout of `LC_ALL=C ls -la --time-style=long-iso` into a
// slice of fileEntry. It skips the leading `total N` size header and any blank lines,
// and keeps `.`/`..` entries as-is (the caller decides how to present them).
//
// The leading columns (modeRaw, link-count, owner, group, size, date, time) are
// whitespace-separated and ls right-aligns size, so runs of spaces appear BETWEEN
// columns. The name is the remainder of the line after the 7th column's trailing
// whitespace; its internal spaces are preserved by locating the name start with
// nameStart (which collapses whitespace only between columns, never inside the name)
// rather than strings.Fields(...)+join.
func parseListing(out string) []fileEntry {
	var entries []fileEntry
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "total ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		modeRaw := fields[0]
		name := line[nameStart(line):]
		e := fileEntry{
			modeRaw: modeRaw,
			isDir:   modeRaw[0] == 'd',
			isLink:  modeRaw[0] == 'l',
			mode:    modeRaw[1:],
			size:    parseSize(fields[4]),
			mtime:   fields[5] + " " + fields[6],
			name:    name,
		}
		if e.isLink {
			if i := strings.Index(name, " -> "); i >= 0 {
				e.name = name[:i]
				e.target = name[i+len(" -> "):]
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// nameStart returns the byte offset in line where the name column begins: it skips the
// first 7 whitespace-separated columns and the whitespace that follows the 7th. Column
// gaps (one or more spaces) are collapsed because whole whitespace runs are skipped;
// spaces inside the name survive because scanning stops once the 7th column's trailing
// whitespace ends.
func nameStart(line string) int {
	i := 0
	n := len(line)
	skipSpaces := func() {
		for i < n && line[i] == ' ' {
			i++
		}
	}
	skipField := func() {
		for i < n && line[i] != ' ' {
			i++
		}
	}
	skipSpaces() // tolerate any leading whitespace
	for c := 0; c < 7; c++ {
		skipField()
		skipSpaces()
	}
	return i
}

// parseSize parses the size column as int64; an unparsable value yields 0.
func parseSize(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
