package gm

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// googleCookies are the cookies libgm sends for Google-account sessions.
var googleCookies = []string{"SID", "HSID", "SSID", "APISID", "SAPISID", "OSID", "__Secure-1PSIDTS"}

var requiredCookies = []string{"SID", "HSID", "SSID", "APISID", "SAPISID"}

var (
	reCurlCookie  = regexp.MustCompile(`(?is)(?:-b|--cookie)\s+(?:'([^']*)'|"([^"]*)")`)
	reCurlHeader  = regexp.MustCompile(`(?is)(?:-H|--header)\s+(?:'cookie:\s*([^']*)'|"cookie:\s*([^"]*)")`)
	reCookiePairs = regexp.MustCompile(`([A-Za-z0-9_\-]+)=([^;\s]*)`)
)

// ParseCookies extracts Google cookies from whatever the user pastes: a
// "Copy as cURL" command (bash or cmd), a Cookie header, a JSON object of
// name→value, or a JSON array exported by a cookie-editor extension.
func ParseCookies(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("nothing pasted")
	}
	all := map[string]string{}
	switch {
	case strings.HasPrefix(raw, "{"):
		var m map[string]string
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("JSON object: %w", err)
		}
		all = m
	case strings.HasPrefix(raw, "["):
		var arr []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
		}
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, fmt.Errorf("JSON array: %w", err)
		}
		for _, c := range arr {
			// Prefer .google.com / messages.google.com over other Google domains.
			if _, seen := all[c.Name]; seen && !strings.HasSuffix(c.Domain, "google.com") {
				continue
			}
			all[c.Name] = c.Value
		}
	default:
		s := raw
		if strings.Contains(s, `^"`) { // Windows cmd "Copy as cURL" escapes
			s = strings.NewReplacer(`^"`, `"`, `^%`, `%`, `^&`, `&`, `^\^`, `^`, "^\n", " ", `^`, ``).Replace(s)
		}
		header := ""
		if strings.HasPrefix(strings.ToLower(s), "curl") {
			for _, re := range []*regexp.Regexp{reCurlCookie, reCurlHeader} {
				for _, m := range re.FindAllStringSubmatch(s, -1) {
					header += m[1] + m[2] + "; "
				}
			}
			if header == "" {
				return nil, errors.New("this cURL command has no cookies; copy the page request to messages.google.com (Network, Doc filter) while signed in")
			}
		} else {
			header = strings.TrimPrefix(strings.TrimPrefix(s, "Cookie:"), "cookie:")
		}
		for _, m := range reCookiePairs.FindAllStringSubmatch(header, -1) {
			all[m[1]] = m[2]
		}
	}
	out := map[string]string{}
	for _, name := range googleCookies {
		if v := strings.TrimSpace(all[name]); v != "" {
			out[name] = v
		}
	}
	var missing []string
	for _, name := range requiredCookies {
		if out[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing Google cookies %s; make sure you are signed in to Google in that window", strings.Join(missing, ", "))
	}
	return out, nil
}
