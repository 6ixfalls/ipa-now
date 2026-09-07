package httpapi

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var bundle = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*(\.[A-Za-z0-9][A-Za-z0-9-]*)+$`)
var appID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var storePath = regexp.MustCompile(`^/(?:[a-z]{2}/)?app/(?:[^/]+/)?id([1-9][0-9]{0,19})/?$`)

func ValidateTarget(raw, source string) (string, error) {
	if len(raw) > 512 || raw != strings.TrimSpace(raw) {
		return "", errors.New("invalid target")
	}
	if source != "installed" && source != "app-store" {
		return "", errors.New("source must be installed or app-store")
	}
	if strings.Contains(raw, "://") {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host != "apps.apple.com" || u.User != nil || u.Fragment != "" {
			return "", errors.New("only HTTPS apps.apple.com app URLs are accepted")
		}
		m := storePath.FindStringSubmatch(u.Path)
		if m == nil {
			return "", errors.New("invalid App Store URL")
		}
		raw = m[1]
	}
	if source == "installed" {
		if !bundle.MatchString(raw) {
			return "", errors.New("installed apps require a bundle ID")
		}
	} else if !bundle.MatchString(raw) && !appID.MatchString(raw) {
		return "", errors.New("enter a bundle ID, App Store ID, or App Store URL")
	}
	if strings.HasSuffix(strings.ToLower(raw), ".ipa") {
		return "", errors.New("use the IPA upload form for files")
	}
	return raw, nil
}
