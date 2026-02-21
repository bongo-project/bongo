//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestContactCRUD(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	// Create a contact.
	resp, err := client.PostForm(baseURL+"/contacts", url.Values{
		"display_name": {"John Doe"},
		"email":        {"john@example.com"},
		"phone":        {"555-1234"},
		"company":      {"Acme Corp"},
		"notes":        {"Test contact"},
	})
	if err != nil {
		t.Fatalf("POST /contacts: %v", err)
	}
	resp.Body.Close()

	// List contacts — should contain John.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("GET /contacts status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "John Doe") {
		t.Error("contact list should contain John Doe")
	}
	if !strings.Contains(string(body), "john@example.com") {
		t.Error("contact list should contain email")
	}

	// Extract GUID from the response (look for data-guid attribute).
	guid := extractGUID(string(body))
	if guid == "" {
		t.Fatal("could not extract contact GUID from response")
	}

	// Update the contact.
	resp, err = client.PostForm(baseURL+"/contacts/"+guid, url.Values{
		"display_name": {"John Updated"},
		"email":        {"john@example.com"},
		"phone":        {"555-5678"},
		"company":      {"Acme Corp"},
		"notes":        {"Updated"},
	})
	if err != nil {
		t.Fatalf("POST /contacts/%s (update): %v", guid, err)
	}
	resp.Body.Close()

	// Verify update.
	resp, err = client.Get(baseURL + "/contacts/" + guid)
	if err != nil {
		t.Fatalf("GET /contacts/%s: %v", guid, err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "555-5678") {
		t.Error("contact detail should contain updated phone")
	}

	// Delete the contact.
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/contacts/"+guid, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /contacts/%s: %v", guid, err)
	}
	resp.Body.Close()

	// Verify deletion — list should not contain John.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts after delete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "John Updated") {
		t.Error("contact list should not contain deleted contact")
	}
}

func TestContactAutocomplete(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	// Create contact.
	resp, err := client.PostForm(baseURL+"/contacts", url.Values{
		"display_name": {"Jane Smith"},
		"email":        {"jane@example.com"},
	})
	if err != nil {
		t.Fatalf("POST /contacts: %v", err)
	}
	resp.Body.Close()

	// Search GAL API for Jane.
	resp, err = client.Get(baseURL + "/api/gal?q=jane")
	if err != nil {
		t.Fatalf("GET /api/gal: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var results []struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Source      string `json:"source"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		t.Fatalf("unmarshal GAL results: %v (body: %s)", err, body)
	}

	found := false
	for _, r := range results {
		if r.Email == "jane@example.com" && r.Source == "contacts" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GAL search should contain jane@example.com from contacts, got: %s", body)
	}
}

func TestVCardImportExport(t *testing.T) {
	_, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	// Create 2 contacts.
	for _, name := range []string{"Alice", "Bob"} {
		resp, err := client.PostForm(baseURL+"/contacts", url.Values{
			"display_name": {name},
			"email":        {strings.ToLower(name) + "@example.com"},
			"phone":        {"555-0000"},
		})
		if err != nil {
			t.Fatalf("POST /contacts (%s): %v", name, err)
		}
		resp.Body.Close()
	}

	// Export.
	resp, err := client.Get(baseURL + "/contacts/export")
	if err != nil {
		t.Fatalf("GET /contacts/export: %v", err)
	}
	vcfData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/vcard") {
		t.Errorf("export Content-Type: got %q, want text/vcard", ct)
	}
	if !strings.Contains(string(vcfData), "Alice") || !strings.Contains(string(vcfData), "Bob") {
		t.Error("exported vCard should contain both contacts")
	}

	// Delete both contacts.
	resp2, _ := client.Get(baseURL + "/contacts")
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	guids := extractAllGUIDs(string(body))
	for _, guid := range guids {
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/contacts/"+guid, nil)
		resp, _ := client.Do(req)
		resp.Body.Close()
	}

	// Verify empty.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts after delete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Alice") {
		t.Error("contacts should be empty after deletion")
	}

	// Import the exported vCard.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("vcf", "contacts.vcf")
	if err != nil {
		t.Fatalf("creating multipart: %v", err)
	}
	part.Write(vcfData)
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/contacts/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST /contacts/import: %v", err)
	}
	resp.Body.Close()

	// Verify imported contacts appear.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts after import: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "Alice") {
		t.Error("contacts should contain Alice after import")
	}
	if !strings.Contains(string(body), "Bob") {
		t.Error("contacts should contain Bob after import")
	}
}

func TestCardDAVSync(t *testing.T) {
	a, baseURL, client, cancel := startDragonflyAgent(t)
	defer cancel()

	userID := findUserID(t, a.Store)
	if userID == "" {
		t.Fatal("could not find user ID")
	}

	// Create a contact via web UI.
	resp, err := client.PostForm(baseURL+"/contacts", url.Values{
		"display_name": {"CardDAV Test"},
		"email":        {"carddav@example.com"},
	})
	if err != nil {
		t.Fatalf("POST /contacts: %v", err)
	}
	resp.Body.Close()

	// Extract GUID.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	guid := extractGUID(string(body))
	if guid == "" {
		t.Fatal("could not extract contact GUID")
	}

	// Create a basic auth client for CardDAV.
	carddavClient := &http.Client{}

	// GET the contact via CardDAV.
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/carddav/%s/addressbooks/default/%s.vcf", baseURL, userID, guid), nil)
	req.SetBasicAuth("admin@localhost", "admin")
	resp, err = carddavClient.Do(req)
	if err != nil {
		t.Fatalf("CardDAV GET: %v", err)
	}
	vcfBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("CardDAV GET status: %d, body: %s", resp.StatusCode, vcfBody)
	}
	if !strings.Contains(string(vcfBody), "CardDAV Test") {
		t.Error("CardDAV GET should return vCard with contact name")
	}

	// PUT a new contact via CardDAV.
	newVCard := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:CardDAV Created\r\nEMAIL:cdcreated@example.com\r\nTEL:555-9999\r\nEND:VCARD\r\n"
	newGUID := "carddav-new-contact"
	req, _ = http.NewRequest("PUT", fmt.Sprintf("%s/carddav/%s/addressbooks/default/%s.vcf", baseURL, userID, newGUID), strings.NewReader(newVCard))
	req.SetBasicAuth("admin@localhost", "admin")
	req.Header.Set("Content-Type", "text/vcard")
	resp, err = carddavClient.Do(req)
	if err != nil {
		t.Fatalf("CardDAV PUT: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		t.Fatalf("CardDAV PUT status: %d", resp.StatusCode)
	}

	// Verify CardDAV-created contact appears in web UI.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts after CardDAV PUT: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "CardDAV Created") {
		t.Error("web UI should show CardDAV-created contact")
	}

	// DELETE contact via CardDAV.
	req, _ = http.NewRequest("DELETE", fmt.Sprintf("%s/carddav/%s/addressbooks/default/%s.vcf", baseURL, userID, newGUID), nil)
	req.SetBasicAuth("admin@localhost", "admin")
	resp, err = carddavClient.Do(req)
	if err != nil {
		t.Fatalf("CardDAV DELETE: %v", err)
	}
	delBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("CardDAV DELETE status: %d, body: %s", resp.StatusCode, delBody)
	}
	t.Logf("CardDAV DELETE status: %d", resp.StatusCode)

	// Verify deleted from web UI.
	resp, err = client.Get(baseURL + "/contacts")
	if err != nil {
		t.Fatalf("GET /contacts after CardDAV DELETE: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "CardDAV Created") {
		t.Error("web UI should not show CardDAV-deleted contact")
	}
}

// extractGUID extracts the first data-guid value from HTML.
func extractGUID(html string) string {
	const marker = `data-guid="`
	idx := strings.Index(html, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], `"`)
	if end < 0 {
		return ""
	}
	return html[start : start+end]
}

// extractAllGUIDs extracts all data-guid values from HTML.
func extractAllGUIDs(html string) []string {
	var guids []string
	const marker = `data-guid="`
	for {
		idx := strings.Index(html, marker)
		if idx < 0 {
			break
		}
		start := idx + len(marker)
		end := strings.Index(html[start:], `"`)
		if end < 0 {
			break
		}
		guids = append(guids, html[start:start+end])
		html = html[start+end:]
	}
	return guids
}

