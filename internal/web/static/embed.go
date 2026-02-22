package static

import "embed"

//go:embed css/*.css js/*.js img/*
var Files embed.FS
