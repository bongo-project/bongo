package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-go/bongo-go/internal/cluster"
)

type mockMemberLister struct {
	members []cluster.MemberInfo
}

func (m *mockMemberLister) Members() []cluster.MemberInfo {
	return m.members
}

func TestClusterMembers(t *testing.T) {
	lister := &mockMemberLister{
		members: []cluster.MemberInfo{
			{Name: "node-1", Addr: "10.0.0.1", Port: 8301, Role: "server", Status: "alive"},
			{Name: "node-2", Addr: "10.0.0.2", Port: 8301, Role: "server", Status: "alive"},
		},
	}

	h := NewClusterHandler(lister)

	req := httptest.NewRequest(http.MethodGet, "/internal/cluster/members", nil)
	w := httptest.NewRecorder()
	h.Members(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var members []cluster.MemberInfo
	if err := json.NewDecoder(w.Body).Decode(&members); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0].Name != "node-1" {
		t.Errorf("first member = %q, want %q", members[0].Name, "node-1")
	}
}

func TestClusterMembersNilLister(t *testing.T) {
	h := NewClusterHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/internal/cluster/members", nil)
	w := httptest.NewRecorder()
	h.Members(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
