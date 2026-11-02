package store

import (
	"errors"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newTaskTestEnv(t *testing.T) (*TaskStore, *PebbleStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewTaskStore(ps, &directApplier{store: ps}), ps
}

func TestTaskStore_CRUD(t *testing.T) {
	ts, ps := newTaskTestEnv(t)

	guid, err := ts.Create("u1", &bongov1.Task{CalendarId: "tasks", Summary: "Milk", Priority: 5, IcalRaw: []byte("raw")})
	if err != nil || guid == "" {
		t.Fatalf("Create: %q, %v", guid, err)
	}
	if _, err := ps.Get([]byte("/user/u1/task/" + guid)); err != nil {
		t.Errorf("task not at /user/{id}/task/{guid}: %v", err)
	}
	got, err := ts.Get("u1", guid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "Milk" || got.Priority != 5 || got.CreatedAt == 0 || got.UpdatedAt == 0 || string(got.IcalRaw) != "raw" {
		t.Errorf("round trip = %+v", got)
	}

	got.Summary = "Bread"
	if err := ts.Update("u1", guid, got); err != nil {
		t.Fatal(err)
	}
	if again, _ := ts.Get("u1", guid); again.Summary != "Bread" {
		t.Errorf("update lost: %+v", again)
	}

	if _, err := ts.Create("u2", &bongov1.Task{Guid: "other", Summary: "Not yours"}); err != nil {
		t.Fatal(err)
	}
	list, err := ts.ListAll("u1")
	if err != nil || len(list) != 1 || list[0].Guid != guid {
		t.Errorf("ListAll(u1) = %v, %v", list, err)
	}

	if err := ts.Delete("u1", guid); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Get("u1", guid); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("Get after delete = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskRecordSizeMatchesStoredSize(t *testing.T) {
	ts, ps := newTaskTestEnv(t)
	task := &bongov1.Task{CalendarId: "tasks", Summary: "s", IcalRaw: make([]byte, 321)}
	want := TaskRecordSize(task)
	guid, err := ts.Create("u1", task)
	if err != nil {
		t.Fatal(err)
	}
	val, err := ps.Get([]byte("/user/u1/task/" + guid))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(val)) != want {
		t.Errorf("stored %d bytes, estimate %d", len(val), want)
	}
}
