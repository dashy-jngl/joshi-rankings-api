package scraper

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// realRow is a verbatim row captured from Momo Watanabe's match history
// (?id=2&nr=15561&page=4) on 2026-08-17, showing Cagematch's anti-scraping
// injections: zero-width space entities and team-stable parentheses.
const realRow = `<table><tr class="TRow2 TRowOnlineStream"><td class="TCol AlignCenter TextLowlight">8</td><td class="TCol TColSeparator">11.07.2026</td><td class="TCol TColSeparator"><a href="?id=8&amp;nr=745"><img src="/x.gif" alt="World Wonder Ring Stardom" /></a></td><td class="TCol TColSeparator">
<span class="MatchCard"><a href="?id=29&amp;nr=3144&amp;name=Gods+Eye">God's Eye</a> (<a href="?id=2&amp;nr=22840&amp;name=Ami+Sourei">Ami Sourei</a>, <a href="?id=2&amp;nr=20443&amp;name=Hina">Hina</a> &amp; <a href="?id=2&amp;nr=21148&amp;name=Tomoka+Inaba">Tomoka Inaba</a>) defeat <a href="?id=29&amp;nr=4140&amp;name=HATE++">HATE</a> (<a href="?id=2&amp;nr=3709&amp;name=Fukigen+Death">Fukigen Death</a>, <a href="?id=2&amp;nr=16624&amp;name=Konami+">Konami</a> &amp; <a href="?id=2&amp;nr=15561&amp;name=Momo+Watanabe">Momo Watanabe</a>) (8:35)</span><div class="MatchEventLine"><a href="?id=1&amp;nr=458023">St&#8203;ardom</a> - Online Stream @ Fujisan Messe in Fuji, Shizuoka, &#8203;Japan</div></td></tr></table>`

// decoyRow exercises the hidden-element decoys: fake rating numbers spliced
// mid-word into the event name and card, exactly as served by Cagematch.
const decoyRow = `<table><tr class="TRow1"><td class="TCol AlignCenter TextLowlight">3</td><td class="TCol TColSeparator">30.06.2026</td><td class="TCol TColSeparator"></td><td class="TCol TColSeparator">
<span class="MatchCard"><a href="?id=2&amp;nr=15561&amp;name=Momo+Watanabe">Mo<small style="display:none">2.44</small>mo Watanabe</a> defeats <a href="?id=2&amp;nr=20600&amp;name=Suzu+Suzuki">Suzu Suzuki</a> <!-- please do not scrape us -->(12:20)</span><div class="MatchEventLine"><a href="?id=1&amp;nr=451504">Stardo<small style="display:none">9.85</small>m Nighter In Korakuen</a> - Online Stream @ Ko<span id="c-x" style="visibility:hidden;position:absolute;z-index:-1">7.1</span>rakuen Hall in Tokyo, Japan</div></td></tr></table>`

// parseRowsFromHTML runs a raw HTML fragment through the same sanitization
// pipeline as fetchPage, then parses match rows.
func parseRowsFromHTML(t *testing.T, html string) []RawMatch {
	t.Helper()
	body := stripInvisibleChars(html)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	removeHiddenElements(doc)
	s := &CagematchScraper{}
	var matches []RawMatch
	s.parseMatchRows(doc, &matches)
	return matches
}

func TestParseStableParensMatch(t *testing.T) {
	matches := parseRowsFromHTML(t, realRow)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]

	if len(m.Participants) != 6 {
		var names []string
		for _, p := range m.Participants {
			names = append(names, p.Name)
		}
		t.Fatalf("expected 6 participants, got %d: %v", len(m.Participants), names)
	}
	for _, p := range m.Participants {
		if strings.ContainsAny(p.Name, "()") || strings.Contains(p.Name, ":") {
			t.Errorf("junk participant name: %q", p.Name)
		}
	}

	// Winners: God's Eye side
	winners := map[string]bool{"Ami Sourei": true, "Hina": true, "Tomoka Inaba": true}
	for _, p := range m.Participants {
		if p.IsWinner != winners[p.Name] {
			t.Errorf("%s: IsWinner=%v, want %v", p.Name, p.IsWinner, winners[p.Name])
		}
	}

	if m.MatchTime != "8:35" {
		t.Errorf("MatchTime = %q, want 8:35", m.MatchTime)
	}
	// Zero-width space stripped from event name
	if m.EventName != "Stardom" {
		t.Errorf("EventName = %q, want Stardom", m.EventName)
	}
}

func TestParseHiddenDecoyMatch(t *testing.T) {
	matches := parseRowsFromHTML(t, decoyRow)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	m := matches[0]

	if m.EventName != "Stardom Nighter In Korakuen" {
		t.Errorf("EventName = %q, want Stardom Nighter In Korakuen", m.EventName)
	}
	if m.Venue != "Korakuen Hall" {
		t.Errorf("Venue = %q, want Korakuen Hall", m.Venue)
	}
	if len(m.Participants) != 2 {
		var names []string
		for _, p := range m.Participants {
			names = append(names, p.Name)
		}
		t.Fatalf("expected 2 participants, got %d: %v", len(m.Participants), names)
	}
	for _, p := range m.Participants {
		wantWin := p.Name == "Momo Watanabe"
		if p.IsWinner != wantWin {
			t.Errorf("%s: IsWinner=%v, want %v", p.Name, p.IsWinner, wantWin)
		}
		if strings.ContainsAny(p.Name, "0123456789") {
			t.Errorf("decoy digits leaked into name: %q", p.Name)
		}
	}
}

func TestExtractGhostNames(t *testing.T) {
	linked := map[string]bool{"Hazuki": true, "Koguma": true}
	cases := []struct {
		name string
		html string
		want []string
	}{
		{
			name: "stable parens leftover is dropped",
			html: `<a href="?id=29&amp;nr=1">Team A</a> (<a href="?id=2&amp;nr=2">Hazuki</a> &amp; <a href="?id=2&amp;nr=3">Koguma</a>) defeat <a href="?id=29&amp;nr=4">Team B</a> (<a href="?id=2&amp;nr=5">X</a>) (9:10)`,
			want: nil,
		},
		{
			name: "draw result tokens with time are dropped",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> vs. <a href="?id=2&amp;nr=3">Koguma</a> - Time Limit Draw (20:20)`,
			want: nil,
		},
		{
			name: "by DQ with time is dropped",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> defeats <a href="?id=2&amp;nr=3">Koguma</a> by DQ (9:06)`,
			want: nil,
		},
		{
			name: "real unlinked names are kept",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> &amp; Kaori Yoneyama defeat Fukigen Death &amp; <a href="?id=2&amp;nr=3">Koguma</a> (10:00)`,
			want: []string{"Kaori Yoneyama", "Fukigen Death"},
		},
		{
			name: "unlinked name with trailing time is cleaned",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> defeats Shion Kanzaki (7:15)`,
			want: []string{"Shion Kanzaki"},
		},
		{
			name: "decoy number leftovers are dropped",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> defeats <a href="?id=2&amp;nr=3">Koguma</a> 9.85 (7:15)`,
			want: []string{"9.85"}[:0],
		},
		{
			name: "champion marker is not a ghost",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> (c) defeats <a href="?id=2&amp;nr=3">Koguma</a> (13:43)`,
			want: nil,
		},
		{
			name: "KO prefix does not swallow Koguma",
			html: `<a href="?id=2&amp;nr=2">Hazuki</a> defeats Kogura (5:00)`,
			want: []string{"Kogura"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractGhostNames(tc.html, linked)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestStripInvisibleChars(t *testing.T) {
	in := "St\u200Bardom &#8203;Nighter &#x200B;In K\u2060orakuen&#8288; \uFEFFHall"
	got := stripInvisibleChars(in)
	if got != "Stardom Nighter In Korakuen Hall" {
		t.Errorf("got %q", got)
	}
}
