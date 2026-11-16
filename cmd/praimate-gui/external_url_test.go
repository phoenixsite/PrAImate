package main

import "testing"

func TestExternalURLAllowsOnlyWebAndMail(t *testing.T) {
	for _, ok := range []string{
		"https://example.com/a?b=c#d",
		"http://localhost:8080/x",
		"  https://example.com  ",
		"mailto:someone@example.com",
	} {
		if _, err := externalURL(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"vscode://file/etc/passwd",
		"steam://run/1",
		"ftp://example.com",
		"https://",
		"/relative/path",
		"#anchor",
		"mailto:",
		"https://example.com/\nsecond",
		"https://example.com/\x00",
		"http://exa mple.com/path",
	} {
		if _, err := externalURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestExternalURLRejectionMessages(t *testing.T) {
	for link, want := range map[string]string{
		"":                         "the link is empty",
		"https://":                 "the link has no host",
		"mailto:":                  "the mail link has no address",
		"http://exa mple.com/path": "the link is not a valid URL",
		"https://a.com/\n":         "", // trailing newline is trimmed: accepted
		"https://a.com/\x01b":      "the link contains control characters",
		"file:///etc/passwd":       "only http, https and mailto links can be opened",
	} {
		_, err := externalURL(link)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q rejected: %v", link, err)
		case want != "" && (err == nil || err.Error() != want):
			t.Errorf("%q: got %v, want %q", link, err, want)
		}
	}
}
