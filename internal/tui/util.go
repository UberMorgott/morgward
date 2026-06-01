package tui

import (
	"net"
	"strconv"
	"strings"
)

// stepFocus advances cur by dir (+1/-1) within the ordered rows slice, wrapping
// around. If cur isn't in rows (e.g. it was just hidden), it lands on the first
// row so focus is never stranded.
func stepFocus(rows []int, cur, dir int) int {
	at := -1
	for i, r := range rows {
		if r == cur {
			at = i
			break
		}
	}
	if at < 0 {
		return rows[0]
	}
	n := len(rows)
	return rows[(at+dir+n)%n]
}

// validHost accepts an IP literal or a syntactically valid hostname.
func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			ok := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') || r == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}

// fsSafeHost makes a host string safe for use in a log filename: the form already
// restricts Host to [0-9a-zA-Z.-] (sanitizeField), so only the dot needs replacing;
// colon is handled too for defensiveness (e.g. a future IPv6 literal).
func fsSafeHost(h string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(h)
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
