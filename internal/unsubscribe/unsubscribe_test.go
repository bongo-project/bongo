package unsubscribe

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
)

func TestParse(t *testing.T) {
	const one = "List-Unsubscribe=One-Click"
	tests := []struct {
		name     string
		unsub    []string
		post     []string
		https    string
		mailto   string
		oneClick bool
	}{
		{"https and mailto", []string{"<mailto:u@list.test?subject=unsub>, <https://list.test/u/abc>"}, []string{one}, "https://list.test/u/abc", "mailto:u@list.test?subject=unsub", true},
		{"folded and spaced", []string{"<mailto:u@list.test> ,\r\n <https://list.test/u/\r\n abc>"}, nil, "https://list.test/u/abc", "mailto:u@list.test", false},
		{"comment outside brackets", []string{"(see <http://evil.test/>) <https://list.test/x>"}, nil, "https://list.test/x", "", false},
		{"http only", []string{"<http://list.test/u>"}, []string{one}, "", "", false},
		{"javascript and data", []string{"<javascript:alert(1)>, <data:text/html,x>"}, []string{one}, "", "", false},
		{"user info", []string{"<https://user:pw@list.test/u>"}, []string{one}, "", "", false},
		{"port 8080", []string{"<https://list.test:8080/u>"}, []string{one}, "", "", false},
		{"port 8443", []string{"<https://list.test:8443/u>"}, []string{one}, "https://list.test:8443/u", "", true},
		{"no host", []string{"<https:///u>"}, nil, "", "", false},
		{"first https wins", []string{"<https://a.test/1>, <https://b.test/2>"}, nil, "https://a.test/1", "", false},
		{"upper-case scheme", []string{"<HTTPS://List.Test/u>"}, []string{one}, "https://List.Test/u", "", true},
		{"two unsubscribe headers", []string{"<https://evil.test/u>", "<https://list.test/u>"}, []string{one}, "https://evil.test/u", "", false},
		{"two post headers", []string{"<https://list.test/u>"}, []string{one, one}, "https://list.test/u", "", false},
		{"post with extra text", []string{"<https://list.test/u>"}, []string{one + "; x=1"}, "https://list.test/u", "", false},
		{"post wrong value", []string{"<https://list.test/u>"}, []string{"List-Unsubscribe=Yes"}, "https://list.test/u", "", false},
		{"post case and space", []string{"<https://list.test/u>"}, []string{"  list-unsubscribe=one-click "}, "https://list.test/u", "", true},
		{"oversized header", []string{"<https://list.test/" + strings.Repeat("a", 3000) + ">"}, []string{one}, "", "", false},
		{"too many entries", []string{strings.Repeat("<mailto:a@b.test>, ", 8) + "<https://list.test/late>"}, nil, "", "mailto:a@b.test", false},
		{"unterminated", []string{"<https://list.test/u"}, nil, "", "", false},
		{"non ascii", []string{"<https://list.test/é>"}, nil, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.unsub, tt.post)
			gotHTTPS := ""
			if got.HTTPS != nil {
				gotHTTPS = got.HTTPS.String()
			}
			if gotHTTPS != tt.https {
				t.Errorf("https = %q, want %q", gotHTTPS, tt.https)
			}
			if got.Mailto != tt.mailto {
				t.Errorf("mailto = %q, want %q", got.Mailto, tt.mailto)
			}
			if got.OneClick != tt.oneClick {
				t.Errorf("oneClick = %v, want %v", got.OneClick, tt.oneClick)
			}
		})
	}
}

func TestParseMailto(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Mailto
		ok   bool
	}{
		{"plain", "mailto:unsub@list.test", Mailto{To: "unsub@list.test"}, true},
		{"subject and body", "mailto:u@list.test?subject=Please%20unsubscribe&body=remove+me%0Athanks", Mailto{To: "u@list.test", Subject: "Please unsubscribe", Body: "remove+me\nthanks"}, true},
		{"cc bcc to dropped", "mailto:u@list.test?cc=a@x.test&bcc=b@x.test&to=c@x.test&subject=s", Mailto{To: "u@list.test", Subject: "s"}, true},
		{"upper-case scheme and key", "MAILTO:u@list.test?SUBJECT=s", Mailto{To: "u@list.test", Subject: "s"}, true},
		{"encoded address", "mailto:u%40list.test", Mailto{To: "u@list.test"}, true},
		{"no address", "mailto:?subject=s", Mailto{}, false},
		{"bad escape in field", "mailto:u@list.test?subject=%zz&body=b", Mailto{To: "u@list.test", Body: "b"}, true},
		{"not mailto", "https://list.test/", Mailto{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseMailto(tt.in)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ParseMailto(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}

const baseHeaders = "From: list@list.test\r\nTo: me@example.test\r\nSubject: news\r\n" +
	"List-Unsubscribe: <https://list.test/u/abc>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"

// signed returns headers + body signed over signKeys, and a key lookup stub.
func signed(t *testing.T, headers string, signKeys []string) ([]byte, Lookup) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	txt := "v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)
	msg := headers + "\r\nhello body\r\n"
	var out strings.Builder
	err = dkim.Sign(&out, strings.NewReader(msg), &dkim.SignOptions{
		Domain: "list.test", Selector: "s1", Signer: priv, HeaderKeys: signKeys,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed, BodyCanonicalization: dkim.CanonicalizationRelaxed,
	})
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(_ context.Context, domain string) ([]string, error) {
		if domain != "s1._domainkey.list.test" {
			return nil, errors.New("no such record")
		}
		return []string{txt}, nil
	}
	return []byte(out.String()), lookup
}

func TestVerifyOneClick(t *testing.T) {
	both := []string{"From", "To", "Subject", "List-Unsubscribe", "List-Unsubscribe-Post"}
	t.Run("covers both", func(t *testing.T) {
		raw, lk := signed(t, baseHeaders, both)
		if !VerifyOneClick(context.Background(), raw, lk) {
			t.Fatal("want true")
		}
	})
	t.Run("covers only List-Unsubscribe", func(t *testing.T) {
		raw, lk := signed(t, baseHeaders, []string{"From", "To", "Subject", "List-Unsubscribe"})
		if VerifyOneClick(context.Background(), raw, lk) {
			t.Fatal("want false")
		}
	})
	t.Run("body altered", func(t *testing.T) {
		raw, lk := signed(t, baseHeaders, both)
		raw = []byte(strings.Replace(string(raw), "hello body", "other body", 1))
		if VerifyOneClick(context.Background(), raw, lk) {
			t.Fatal("want false")
		}
	})
	t.Run("signed header altered", func(t *testing.T) {
		raw, lk := signed(t, baseHeaders, both)
		raw = []byte(strings.Replace(string(raw), "list.test/u/abc", "evil.test/u/abc", 1))
		if VerifyOneClick(context.Background(), raw, lk) {
			t.Fatal("want false")
		}
	})
	t.Run("lookup error", func(t *testing.T) {
		raw, _ := signed(t, baseHeaders, both)
		fail := func(context.Context, string) ([]string, error) { return nil, errors.New("servfail") }
		if VerifyOneClick(context.Background(), raw, fail) {
			t.Fatal("want false")
		}
	})
	t.Run("lookup honours the deadline", func(t *testing.T) {
		raw, _ := signed(t, baseHeaders, both)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		slow := func(c context.Context, _ string) ([]string, error) {
			<-c.Done()
			return nil, c.Err()
		}
		start := time.Now()
		if VerifyOneClick(ctx, raw, slow) {
			t.Fatal("want false")
		}
		if time.Since(start) > 2*time.Second {
			t.Fatal("lookup was not bounded by the context")
		}
	})
	t.Run("unsigned message", func(t *testing.T) {
		raw := []byte(baseHeaders + "\r\nbody\r\n")
		if VerifyOneClick(context.Background(), raw, nil) {
			t.Fatal("want false")
		}
	})
	t.Run("extra unsigned header above the signed one", func(t *testing.T) {
		// DKIM takes the bottom instance of a header, so the signature of the
		// original may still verify with a forged header prepended. Either the
		// signature check fails, or Parse sees two List-Unsubscribe headers and
		// refuses one-click: in no case is a one-click offer made overall.
		raw, lk := signed(t, baseHeaders, both)
		forged := []byte("List-Unsubscribe: <https://evil.test/u>\r\n" + string(raw))
		msg, err := mail.ReadMessage(bytes.NewReader(forged))
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		opts := Parse(msg.Header["List-Unsubscribe"], msg.Header["List-Unsubscribe-Post"])
		if n := len(msg.Header["List-Unsubscribe"]); n != 2 {
			t.Fatalf("forged message has %d List-Unsubscribe headers, want 2", n)
		}
		if opts.OneClick {
			t.Fatal("two List-Unsubscribe headers must not offer one-click, whether or not the signature verifies")
		}
		t.Logf("signature verifies with the prepended duplicate: %v", VerifyOneClick(context.Background(), forged, lk))
	})
}
