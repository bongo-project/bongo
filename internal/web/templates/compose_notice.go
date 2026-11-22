package templates

import "context"

type composeNoticeKey struct{}

// WithComposeNotice sets a short notice the compose form shows under the To
// field, such as a contact group that is larger than the recipient limit.
func WithComposeNotice(ctx context.Context, notice string) context.Context {
	return context.WithValue(ctx, composeNoticeKey{}, notice)
}

func composeNotice(ctx context.Context) string {
	s, _ := ctx.Value(composeNoticeKey{}).(string)
	return s
}
