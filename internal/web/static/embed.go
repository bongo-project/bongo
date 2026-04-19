package static

import "embed"

//go:embed css/*.css css/componentlib/*.css js/*.js img/* fonts/*
var Files embed.FS
