package templates

// ForwardTargetInfo is one external forwarding target of the signed-in user.
// State is "pending", "verified" or "grandfathered".
type ForwardTargetInfo struct {
	Address string
	State   string
}

// ForwardView is what the settings page shows about forwarding verification.
type ForwardView struct {
	Targets []ForwardTargetInfo
	Error   string
}
