package core

import (
	"fmt"
	"regexp"
	"strings"
)

var defaultVersionRE = regexp.MustCompile(`v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?)`)

// ExtractVersion pulls a real version string out of a kernel's native `-v`
// output. If regex is non-empty it is used verbatim and its first capture
// group (or the whole match) is returned; otherwise a permissive semver-ish
// pattern is used. An unparsable output is an error, never a fallback string —
// NGG must never display an invented version.
func ExtractVersion(raw, regex string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty version output")
	}
	if regex != "" {
		re, err := regexp.Compile(regex)
		if err != nil {
			return "", fmt.Errorf("invalid version regex: %w", err)
		}
		m := re.FindStringSubmatch(raw)
		if m == nil {
			return "", fmt.Errorf("version regex %q did not match %q", regex, firstLine(raw))
		}
		if len(m) > 1 && m[1] != "" {
			return m[1], nil
		}
		return m[0], nil
	}
	m := defaultVersionRE.FindStringSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("no version token found in %q", firstLine(raw))
	}
	return m[1], nil
}

// ExtractVersionAny tries an ordered list of regexes and falls back to the
// generic semver extractor. It returns an error (never a fabricated version)
// when nothing matches.
func ExtractVersionAny(raw string, regexes ...string) (string, error) {
	var last error
	for _, r := range regexes {
		v, err := ExtractVersion(raw, r)
		if err == nil {
			return v, nil
		}
		last = err
	}
	v, err := ExtractVersion(raw, "")
	if err == nil {
		return v, nil
	}
	if last != nil {
		return "", fmt.Errorf("%v; generic: %w", last, err)
	}
	return "", err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 160 {
		return s[:160]
	}
	return s
}
