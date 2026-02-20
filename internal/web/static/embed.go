package static

import "embed"

//go:embed css/*.css js/*.js
var Files embed.FS
