// Package pkgver compares package versions: dpkg (CompareDeb) and rpm (CompareRPM) semantics. Shared by the checker
// (pkg candidates) and the verifier (advisory matching, plan).
package pkgver

import (
	"strconv"
	"strings"
	"unicode"
)

// splitEpoch returns epoch, version-release.
func splitEpoch(v string) (int, string) {
	if i := strings.Index(v, ":"); i > 0 {
		if e, err := strconv.Atoi(v[:i]); err == nil {
			return e, v[i+1:]
		}
	}
	return 0, v
}

// CompareDeb implements dpkg --compare-versions semantics (epoch, upstream, revision; ~ sorts first).
func CompareDeb(a, b string) int {
	ea, ra := splitEpoch(a)
	eb, rb := splitEpoch(b)
	if ea != eb {
		return cmpInt(ea, eb)
	}
	ua, da := splitRev(ra)
	ub, db := splitRev(rb)
	if c := debPart(ua, ub); c != 0 {
		return c
	}
	return debPart(da, db)
}

func splitRev(v string) (string, string) {
	if i := strings.LastIndex(v, "-"); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func debOrder(c byte) int {
	switch {
	case c == '~':
		return -1
	case c == 0:
		return 0
	case unicode.IsLetter(rune(c)):
		return int(c)
	}
	return int(c) + 256
}

func debPart(a, b string) int {
	for a != "" || b != "" {
		// non-digit prefix
		for (a != "" && !isDigit(a[0])) || (b != "" && !isDigit(b[0])) {
			var ca, cb byte
			if a != "" && !isDigit(a[0]) {
				ca = a[0]
			}
			if b != "" && !isDigit(b[0]) {
				cb = b[0]
			}
			oa, ob := debOrder(ca), debOrder(cb)
			if oa != ob {
				return cmpInt(oa, ob)
			}
			if ca != 0 {
				a = a[1:]
			}
			if cb != 0 {
				b = b[1:]
			}
		}
		na, nb := 0, 0
		for a != "" && isDigit(a[0]) {
			na = na*10 + int(a[0]-'0')
			a = a[1:]
		}
		for b != "" && isDigit(b[0]) {
			nb = nb*10 + int(b[0]-'0')
			b = b[1:]
		}
		if na != nb {
			return cmpInt(na, nb)
		}
	}
	return 0
}

// CompareRPM implements rpmvercmp on EVR (epoch defaults to 0).
func CompareRPM(a, b string) int {
	ea, ra := splitEpoch(a)
	eb, rb := splitEpoch(b)
	if ea != eb {
		return cmpInt(ea, eb)
	}
	va, rla := splitRev(ra)
	vb, rlb := splitRev(rb)
	if c := rpmvercmp(va, vb); c != 0 {
		return c
	}
	if rla == "" || rlb == "" {
		return 0 // fixed "7.4.33" with no release matches any release
	}
	return rpmvercmp(rla, rlb)
}

func rpmvercmp(a, b string) int {
	if a == b {
		return 0
	}
	for {
		for a != "" && !isAlnum(a[0]) && a[0] != '~' && a[0] != '^' {
			a = a[1:]
		}
		for b != "" && !isAlnum(b[0]) && b[0] != '~' && b[0] != '^' {
			b = b[1:]
		}
		if (a != "" && a[0] == '~') || (b != "" && b[0] == '~') {
			if a == "" || a[0] != '~' {
				return 1
			}
			if b == "" || b[0] != '~' {
				return -1
			}
			a, b = a[1:], b[1:]
			continue
		}
		if a == "" || b == "" {
			break
		}
		var sa, sb string
		num := isDigit(a[0])
		if num {
			sa, a = takeWhile(a, isDigit)
			sb, b = takeWhile(b, isDigit)
		} else {
			sa, a = takeWhile(a, isAlpha)
			sb, b = takeWhile(b, isAlpha)
		}
		if sb == "" {
			if num {
				return 1
			}
			return -1
		}
		if num {
			sa, sb = strings.TrimLeft(sa, "0"), strings.TrimLeft(sb, "0")
			if len(sa) != len(sb) {
				return cmpInt(len(sa), len(sb))
			}
		}
		if c := strings.Compare(sa, sb); c != 0 {
			return c
		}
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}

func takeWhile(s string, f func(byte) bool) (string, string) {
	i := 0
	for i < len(s) && f(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
