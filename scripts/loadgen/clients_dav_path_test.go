package main

import (
	"net/http"
	"testing"
)

func TestWithCollectionSlash(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/caldav", "/caldav/"},
		{"/carddav", "/carddav/"},
		{"/caldav/", "/caldav/"},
		{"/caldav/u/calendars/default/", "/caldav/u/calendars/default/"},
		{"/caldavx", "/caldavx"},
	} {
		req, err := http.NewRequest("PROPFIND", "https://127.0.0.1:1"+tc.in, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := withCollectionSlash(req)
		if got.URL.Path != tc.want {
			t.Errorf("withCollectionSlash(%q) path = %q, want %q", tc.in, got.URL.Path, tc.want)
		}
		if req.URL.Path != tc.in {
			t.Errorf("the caller's request was changed: %q -> %q", tc.in, req.URL.Path)
		}
	}
}

func TestDAVObjectPath(t *testing.T) {
	for _, tc := range []struct{ collection, name, want string }{
		{"/caldav/u/calendars/default/", "x.ics", "/caldav/u/calendars/default/x.ics"},
		{"/caldav/u/calendars/default", "x.ics", "/caldav/u/calendars/default/x.ics"},
		{"/carddav/u/contacts/", "y.vcf", "/carddav/u/contacts/y.vcf"},
	} {
		if got := davObjectPath(tc.collection, tc.name); got != tc.want {
			t.Errorf("davObjectPath(%q, %q) = %q, want %q", tc.collection, tc.name, got, tc.want)
		}
	}
}
