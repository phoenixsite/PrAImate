package main

import (
	"errors"
	"net/url"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const maxExternalURLLength = 8192

// externalURL validates a link that model output asked the user to open. Only
// web and mail links are allowed, so a response can never launch file:, custom
// protocol handlers or scripts through the system opener.
func externalURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("the link is empty")
	}
	if len(value) > maxExternalURLLength {
		return "", errors.New("the link is too long")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("the link contains control characters")
		}
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", errors.New("the link is not a valid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Hostname() == "" {
			return "", errors.New("the link has no host")
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return "", errors.New("the mail link has no address")
		}
	default:
		return "", errors.New("only http, https and mailto links can be opened")
	}
	return value, nil
}

// OpenExternalURL opens a link from a chat in the system's default browser (or
// mail client). It is a desktop binding only and is not part of the remote API.
func (a *App) OpenExternalURL(raw string) error {
	value, err := externalURL(raw)
	if err != nil {
		return err
	}
	if a.ctx == nil {
		return errors.New("window is not ready")
	}
	wruntime.BrowserOpenURL(a.ctx, value)
	return nil
}
