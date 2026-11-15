package store

import "time"

// ValidACLKind reports whether kind is a grant kind.
func ValidACLKind(kind string) bool { return validACLKind(kind) }

// SetGrantKeepState writes a grant of any kind. An existing grant keeps its
// stored state, decided in the FSM; state applies only to a new grant. grantee
// is typed (GranteeUser or GranteeGroup).
func (s *ACLStore) SetGrantKeepState(kind, owner, resource, grantee, level, rights, state, grantedBy string, now time.Time) error {
	return s.apply(ACLRequest{
		Op: ACLOpSet, Kind: kind, Owner: owner, Resource: resource, Grantee: grantee,
		Level: level, Rights: rights, State: state, GrantedBy: grantedBy, At: now.Unix(),
		KeepState: true,
	})
}

// SetGrant writes a grant of any kind and overwrites an existing one, state
// included. A group grant is written this way, accepted: a group has no single
// person to accept it. grantee is typed.
func (s *ACLStore) SetGrant(kind, owner, resource, grantee, level, rights, state, grantedBy string, now time.Time) error {
	return s.apply(ACLRequest{
		Op: ACLOpSet, Kind: kind, Owner: owner, Resource: resource, Grantee: grantee,
		Level: level, Rights: rights, State: state, GrantedBy: grantedBy, At: now.Unix(),
	})
}

// AcceptGrant turns a pending grant of any kind into an accepted one. The FSM
// decides: an absent, already accepted or expired grant is left alone.
func (s *ACLStore) AcceptGrant(kind, owner, resource, grantee string, now time.Time) error {
	return s.apply(ACLRequest{
		Op: ACLOpAccept, Kind: kind, Owner: owner, Resource: resource, Grantee: grantee, At: now.Unix(),
	})
}

// RevokeGrant removes one grant of any kind. An absent grant is a no-op.
func (s *ACLStore) RevokeGrant(kind, owner, resource, grantee string) error {
	return s.apply(ACLRequest{Op: ACLOpRevoke, Kind: kind, Owner: owner, Resource: resource, Grantee: grantee})
}
