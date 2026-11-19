package templates

import (
	"strings"
	"testing"
)

// templ sanitises URL attributes and replaces any scheme outside http, https,
// mailto, tel, ftp and ftps with about:invalid#TemplFailedSanitizationURL. The
// QR codes on the two-factor and Add a device pages are server-generated data:
// URIs, so the templates must pass them as templ.SafeURL or the image is lost.
func TestQRDataURIsSurviveURLSanitisation(t *testing.T) {
	const png = "data:image/png;base64,iVBORw0KGgo="

	tests := []struct {
		name string
		html string
	}{
		{"two-factor setup", renderAdmin(t, TOTPSetup("iVBORw0KGgo=", "ABCD EFGH", ""))},
		{"add a device", renderAdmin(t, deviceQR(DeviceView{QR: &DeviceQR{DataURI: png, Hash: "abc"}}))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.html, "TemplFailedSanitizationURL") {
				t.Fatalf("QR image src was sanitised away")
			}
			if !strings.Contains(tt.html, `src="`+png+`"`) {
				t.Fatalf("QR image src %q not found in output", png)
			}
		})
	}
}
