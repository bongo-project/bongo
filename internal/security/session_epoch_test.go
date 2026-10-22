package security

import "testing"

// Pinned before the generation parameter existed: a zero generation must leave
// every existing cookie's epoch untouched, or the upgrade logs everyone out.
func TestSessionEpochZeroGenerationUnchanged(t *testing.T) {
	if got := SessionEpoch([]byte("pw-hash"), []byte("totp-secret"), true, 0); got != "ad4298ff6bd424d8" {
		t.Errorf("2FA epoch = %s, want ad4298ff6bd424d8", got)
	}
	if got := SessionEpoch([]byte("pw-hash"), nil, false, 0); got != "3dc02ed7c780e11f" {
		t.Errorf("plain epoch = %s, want 3dc02ed7c780e11f", got)
	}
}

func TestSessionEpochGenerationChangesEpoch(t *testing.T) {
	base := SessionEpoch([]byte("pw-hash"), nil, false, 0)
	g1 := SessionEpoch([]byte("pw-hash"), nil, false, 1)
	g2 := SessionEpoch([]byte("pw-hash"), nil, false, 2)
	if g1 == base || g2 == base || g1 == g2 {
		t.Errorf("generation must change the epoch: %s %s %s", base, g1, g2)
	}
}
