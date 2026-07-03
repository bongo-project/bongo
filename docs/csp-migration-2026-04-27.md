# CSP Migration Tracker

Date: 2026-04-27

Repository: `bongo-go`

## Goal

- remove `unsafe-eval` from `script-src`
- remove `unsafe-inline` from `script-src`
- remove `unsafe-inline` from `style-src`
- keep the app server-rendered with no npm toolchain

## Result

The CSP migration is complete.

Current policy:

- `script-src 'self'`
- `style-src 'self'`
- `object-src 'none'`

## What Changed

- switched layouts to Alpine's CSP runtime: `internal/web/static/js/alpine-csp.min.js`
- removed inline `<script>` blocks and literal inline event handlers from templates
- moved page-local behavior into `internal/web/static/js/bongo.js`
- removed template inline styles across the web UI
- converted tag/color UI to class-based palette mapping
- converted mailbox thread indentation to depth classes
- replaced app-owned `x-show` usage with `hidden` bindings
- replaced app-owned direct `.style` writes with class-based or `hidden` state updates
- added `internal/web/static/css/generated_geometry.css` for CSP-safe geometry classes

## Final State

- no template `style=` / `:style=` / `x-bind:style` sites remain
- no app-owned `x-show` usage remains
- no app-owned direct `.style` writes remain in `internal/web/static/js/bongo.js`

Repo-owned validation checks:

- `rg -n "style=|:style=|x-bind:style|x-show=" internal/web/templates/*.templ` returns no matches
- `rg -n "\.style\.|\.style\s*=|style\." internal/web --glob '!**/*_templ.go' --glob '!internal/web/static/js/alpine-csp.min.js' --glob '!internal/web/static/js/alpine.min.js' --glob '!internal/web/static/js/sortable.min.js'` returns no matches

## Validation

```sh
templ generate
go test ./...
go test -race ./internal/web/... ./internal/cluster ./internal/agent ./internal/store
```

## Notes

- The presence of `x-data` and `x-on` attributes is not by itself a browser inline-script CSP violation.
- The key enabler was Alpine's CSP-compatible runtime.
- Vendor bundles such as Alpine and Sortable still contain internal style-manipulation code, but the application no longer relies on those style-mutating features for its own UI behavior.
