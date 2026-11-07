package main

import "testing"

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
