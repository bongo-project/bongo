package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CreateNew must never overwrite: Create is a blind PUT, and an overwrite
// would drop the existing calendar's publish token and subscription.
func TestCalendarStore_CreateNew(t *testing.T) {
	const user = "u1"
	tests := []struct {
		name    string
		guid    string
		wantErr error // nil means success; errAny means any non-nil error
	}{
		{name: "new GUID", guid: "fresh"},
		{name: "existing GUID", guid: "existing", wantErr: ErrExists},
		{name: "empty GUID", guid: "", wantErr: errAny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cs := newCalendarTestEnv(t)
			if _, err := cs.Create(user, &bongov1.Calendar{
				Guid: "existing", Name: "Original", Published: true, PublishToken: []byte("tok-original"),
			}); err != nil {
				t.Fatalf("seeding: %v", err)
			}

			err := cs.CreateNew(user, &bongov1.Calendar{Guid: tc.guid, Name: "Replacement"})
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("CreateNew = %v, want nil", err)
			case tc.wantErr == errAny && err == nil:
				t.Fatal("CreateNew = nil, want an error")
			case tc.wantErr != nil && tc.wantErr != errAny && !errors.Is(err, tc.wantErr):
				t.Fatalf("CreateNew = %v, want %v", err, tc.wantErr)
			}

			// The seeded calendar is untouched whatever happened.
			got, err := cs.Get(user, "existing")
			if err != nil {
				t.Fatalf("Get existing: %v", err)
			}
			if got.Name != "Original" || string(got.PublishToken) != "tok-original" || !got.Published {
				t.Errorf("existing calendar changed: name=%q token=%q published=%v", got.Name, got.PublishToken, got.Published)
			}

			if tc.wantErr == nil {
				created, err := cs.Get(user, tc.guid)
				if err != nil {
					t.Fatalf("Get created: %v", err)
				}
				if created.Name != "Replacement" {
					t.Errorf("created name = %q, want Replacement", created.Name)
				}
			}
			if tc.guid == "" {
				cals, err := cs.List(user)
				if err != nil {
					t.Fatal(err)
				}
				if len(cals) != 1 {
					t.Errorf("an empty GUID wrote a calendar: %d calendars", len(cals))
				}
			}
		})
	}
}

var errAny = errors.New("any error")
