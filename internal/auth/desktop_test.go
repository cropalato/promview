package auth

import "testing"

func TestValidateDesktopRedirectAcceptsALoopbackListener(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:53123/callback",
		"http://localhost:53123/callback",
		"http://[::1]:53123/callback",
		"http://127.0.0.1:53123/",
	} {
		if _, err := ValidateDesktopRedirect(raw); err != nil {
			t.Errorf("ValidateDesktopRedirect(%q) error = %v", raw, err)
		}
	}
}

func TestValidateDesktopRedirectRefusesAnythingNotUnmistakablyLocal(t *testing.T) {
	// This is the open-redirect boundary of the flow: the server sends a freshly
	// minted credential to whatever survives this check.
	for _, test := range []struct{ name, raw string }{
		{"empty", ""},
		{"remote host", "http://evil.example:80/callback"},
		{"a name that merely resolves locally today", "http://localtest.me:8080/callback"},
		{"https, which a loopback listener cannot honestly offer", "https://127.0.0.1:53123/callback"},
		{"a non-http scheme", "javascript:alert(1)"},
		{"no port, which would mean port 80 on the user's machine", "http://127.0.0.1/callback"},
		{"a query the callback would have to merge with", "http://127.0.0.1:53123/cb?next=http://evil.example"},
		{"a fragment", "http://127.0.0.1:53123/cb#x"},
		{"credentials", "http://user:pass@127.0.0.1:53123/cb"},
		{"a host that only looks loopback", "http://127.0.0.1.evil.example:80/cb"},
		{"not a url at all", "://"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateDesktopRedirect(test.raw); err == nil {
				t.Fatalf("ValidateDesktopRedirect(%q) error = nil, want refusal", test.raw)
			}
		})
	}
}

func TestDesktopRedirectWithCodeAppendsToThePathItWasGiven(t *testing.T) {
	got, err := DesktopRedirectWithCode("http://127.0.0.1:53123/callback", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:53123/callback?code=abc123" {
		t.Errorf("redirect = %q", got)
	}
}

func TestDesktopRedirectWithCodeEscapesTheCode(t *testing.T) {
	// The code is server-generated, but it reaches a URL either way; encoding
	// it is the difference between a parameter and an injection.
	got, err := DesktopRedirectWithCode("http://127.0.0.1:1/cb", "a&b=c")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:1/cb?code=a%26b%3Dc" {
		t.Errorf("redirect = %q, want the code escaped", got)
	}
}
