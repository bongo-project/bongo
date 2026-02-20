package handlers

import (
	"net/http"

	"github.com/bongo-go/bongo-go/internal/cluster"
)

// ClusterMemberLister returns the current cluster members.
type ClusterMemberLister interface {
	Members() []cluster.MemberInfo
}

// ClusterHandler provides internal cluster endpoints.
type ClusterHandler struct {
	lister ClusterMemberLister
}

// NewClusterHandler creates a new ClusterHandler.
func NewClusterHandler(lister ClusterMemberLister) *ClusterHandler {
	return &ClusterHandler{lister: lister}
}

// Members handles GET /internal/cluster/members — returns cluster membership as JSON.
func (h *ClusterHandler) Members(w http.ResponseWriter, r *http.Request) {
	if h.lister == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "gossip not available"})
		return
	}
	writeJSON(w, http.StatusOK, h.lister.Members())
}
