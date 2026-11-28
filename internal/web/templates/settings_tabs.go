package templates

import (
	"context"
	"fmt"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// settingsTabsData is the Alpine expression for the settings page tab group.
// The forwarding tab is last, so switching it off only lowers the count.
func settingsTabsData(ctx context.Context) string {
	n := 5
	if middleware.FeatureDisabled(ctx, store.FeatureForwarding) {
		n--
	}
	return fmt.Sprintf("settingsTabs(%d)", n)
}
