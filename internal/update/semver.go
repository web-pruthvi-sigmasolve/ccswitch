package update

import (
	"strconv"
	"strings"
)

// semver is a parsed semantic version (build metadata is dropped).
type semver struct {
	major, minor, patch int
	pre                 []string
}

// trimV removes surrounding space and one leading "v".
func trimV(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "v")
}

// parseSemver parses "1.2.3", "v1.2.3", "1.2.3-rc1" and "1.2.3+build".
func parseSemver(s string) (semver, bool) {
	s = trimV(s)
	s, _, _ = strings.Cut(s, "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var nums [3]int
	for i, p := range parts {
		n, ok := atoiStrict(p)
		if !ok {
			return semver{}, false
		}
		nums[i] = n
	}
	v := semver{major: nums[0], minor: nums[1], patch: nums[2]}
	if hasPre {
		if pre == "" {
			return semver{}, false
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return semver{}, false
			}
		}
	}
	return v, true
}

// atoiStrict parses a non-negative decimal number made only of digits.
func atoiStrict(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// compare returns -1, 0 or 1 following semver precedence.
func (a semver) compare(b semver) int {
	for _, d := range [3][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := comparePreID(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// comparePreID orders two pre-release identifiers: numeric ones compare
// numerically and sort before alphanumeric ones.
func comparePreID(a, b string) int {
	an, aNum := atoiStrict(a)
	bn, bNum := atoiStrict(b)
	switch {
	case aNum && bNum:
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
		return 0
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

// isNewer reports whether latest should be offered to someone running
// current. A pre-release latest is ignored unless current is a pre-release.
// Unparseable versions are never newer.
func isNewer(latest, current string) bool {
	l, ok1 := parseSemver(latest)
	c, ok2 := parseSemver(current)
	if !ok1 || !ok2 {
		return false
	}
	if len(l.pre) > 0 && len(c.pre) == 0 {
		return false
	}
	return l.compare(c) > 0
}

// NormalizeVersion returns v without surrounding space or a leading "v" and
// reports whether it is a valid semantic version such as "0.2.0".
func NormalizeVersion(v string) (string, bool) {
	v = trimV(v)
	_, ok := parseSemver(v)
	return v, ok
}
