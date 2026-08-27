package templates

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestContactVCardDetails(t *testing.T) {
	raw := "BEGIN:VCARD\r\n" +
		"VERSION:3.0\r\n" +
		"FN:Jane Doe\r\n" +
		"EMAIL:primary@example.com\r\n" +
		"EMAIL:second@example.com\r\n" +
		"TEL:+1000\r\n" +
		"TEL:+2000\r\n" +
		"ADR:;;1 High St;Metropolis;;99999;Country\r\n" +
		"BDAY:1985-05-05\r\n" +
		"TITLE:Director\r\n" +
		"URL:https://example.com\r\n" +
		"NICKNAME:Janey\r\n" +
		"END:VCARD\r\n"

	extras := contactVCardDetails(&bongov1.Contact{VcardRaw: []byte(raw)})

	// Collected by label key -> value(s).
	got := map[string][]string{}
	for _, f := range extras {
		got[f.LabelKey] = append(got[f.LabelKey], f.Value)
	}

	// Primary email/phone are shown elsewhere; only the extras appear here.
	if len(got["contacts.email"]) != 1 || got["contacts.email"][0] != "second@example.com" {
		t.Errorf("extra emails = %v, want [second@example.com]", got["contacts.email"])
	}
	if len(got["contacts.phone"]) != 1 || got["contacts.phone"][0] != "+2000" {
		t.Errorf("extra phones = %v, want [+2000]", got["contacts.phone"])
	}
	if v := got["contacts.address"]; len(v) != 1 || v[0] != "1 High St, Metropolis, 99999, Country" {
		t.Errorf("address = %v", v)
	}
	for key, want := range map[string]string{
		"contacts.title":    "Director",
		"contacts.birthday": "1985-05-05",
		"contacts.website":  "https://example.com",
		"contacts.nickname": "Janey",
	} {
		if len(got[key]) != 1 || got[key][0] != want {
			t.Errorf("%s = %v, want %q", key, got[key], want)
		}
	}
}

func TestContactVCardDetailsEmpty(t *testing.T) {
	if got := contactVCardDetails(&bongov1.Contact{}); got != nil {
		t.Errorf("no vCard should yield no extras, got %v", got)
	}
}

func TestCommonTimezonesFromSystem(t *testing.T) {
	tz := commonTimezones()
	// The system tz database (or, failing that, the fallback) must yield a
	// substantial list starting with UTC and containing well-known zones.
	if len(tz) < 40 {
		t.Fatalf("got %d timezones, expected a full list", len(tz))
	}
	if tz[0] != "UTC" {
		t.Errorf("first zone = %q, want UTC", tz[0])
	}
	for _, want := range []string{"Europe/London", "America/New_York", "Asia/Tokyo"} {
		found := false
		for _, z := range tz {
			if z == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("timezone %q missing from list", want)
		}
	}
}

func TestNormalizeBirthday(t *testing.T) {
	for in, want := range map[string]string{
		"1985-05-05":           "1985-05-05",
		"19850505":             "1985-05-05",
		"1985-05-05T00:00:00Z": "1985-05-05",
		"1985-05-05 12:00":     "1985-05-05",
		"":                     "",
		"--0505":               "--0505",
	} {
		if got := normalizeBirthday(in); got != want {
			t.Errorf("normalizeBirthday(%q) = %q, want %q", in, got, want)
		}
	}
}
