package bloem

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

type ListLocator struct {
	Kind     string `json:"kind"`
	NativeID string `json:"native_id,omitempty"`
	Username string `json:"username,omitempty"`
	Slug     string `json:"slug,omitempty"`
}

// ParseListLocator recognizes links on the configured instance; it never
// performs network IO or accepts a URL as a new service origin.
func ParseListLocator(origin, link string) (ListLocator, error) {
	invalid := fault.Invalid("Use a list URL from the connected Pastime instance.")
	base, err := url.Parse(origin)
	if err != nil || base.Host == "" || base.Scheme != "https" && base.Scheme != "http" || base.User != nil || base.Path != "" && base.Path != "/" || base.RawQuery != "" || base.Fragment != "" {
		return ListLocator{}, invalid
	}
	u, err := url.Parse(link)
	if err != nil || len(link) > 4096 || !utf8.ValidString(link) || strings.ContainsFunc(link, unicode.IsControl) ||
		u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return ListLocator{}, invalid
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 256 || strings.TrimSpace(part) != part {
			return ListLocator{}, invalid
		}
	}
	if len(parts) == 2 && (parts[0] == "lists" || parts[0] == "collab") {
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != parts[1] {
			return ListLocator{}, invalid
		}
		kind := "personal"
		if parts[0] == "collab" {
			kind = "collaborative"
		}
		return ListLocator{Kind: kind, NativeID: parts[1]}, nil
	}
	if len(parts) == 3 && parts[0] == "u" {
		return ListLocator{Kind: "public", Username: parts[1], Slug: parts[2]}, nil
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "public" && parts[4] == "lists" {
		return ListLocator{Kind: "public", Username: parts[3], Slug: parts[5]}, nil
	}
	return ListLocator{}, invalid
}
