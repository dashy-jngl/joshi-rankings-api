package scraper

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// TestSucuriLive hits the real cagematch site to confirm the challenge solver
// restores access. Run with: go test ./scraper -run TestSucuriLive -v
func TestSucuriLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in -short mode")
	}
	jar, _ := cookiejar.New(nil)
	s := &CagematchScraper{
		baseURL: "https://www.cagematch.net",
		client:  &http.Client{Jar: jar},
	}

	doc, err := s.fetchPage("https://www.cagematch.net/?id=2&nr=10402")
	if err != nil {
		t.Fatalf("fetchPage failed: %v", err)
	}
	name := strings.TrimSpace(doc.Find("h1.TextHeader").First().Text())
	if name == "" {
		t.Fatalf("no TextHeader found — still blocked?")
	}
	t.Logf("Got profile header: %q", name)
	if strings.Contains(name, "redirected") {
		t.Fatalf("still on challenge page")
	}
}
