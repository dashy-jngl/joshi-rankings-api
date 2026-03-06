package scraper

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ResultsScraper interface {
	Name() string
	FetchResults() ([]RawMatch, error)
}

type RawMatch struct {
	MatchType       string
	EventName       string
	Date            time.Time
	IsTitleMatch    bool
	IsDraw          bool
	MatchTime       string  // e.g. "25:09"
	FinishType      string  // e.g. "Pinfall", "Submission", "Count Out"
	Venue           string  // e.g. "Korakuen Hall"
	Location        string  // e.g. "Tokyo, Japan"
	Promotion       string  // promotion that ran the event
	Stipulation     string  // e.g. "No DQ", "Cage Match" — from match type
	CagematchEventID int    // from ?id=1&nr=XXXXX in MatchEventLine
	MatchIndex      int     // row number on the event card
	Participants    []RawParticipant
}

type RawParticipant struct {
	Name        string
	CagematchID int
	Team        int
	IsWinner    bool
}

// BuildMatchKey generates a deterministic composite key for dedup:
// date|event_name|match_type|sorted_participant_cagematch_ids
// This uniquely identifies a match without fuzzy overlap matching.
func BuildMatchKey(raw RawMatch) string {
	ids := make([]string, 0, len(raw.Participants))
	for _, p := range raw.Participants {
		if p.CagematchID > 0 {
			ids = append(ids, fmt.Sprintf("%d", p.CagematchID))
		}
	}
	sort.Strings(ids)

	return fmt.Sprintf("%s|%s|%s|%s|%s",
		raw.Date.Format("2006-01-02"),
		raw.EventName,
		strings.ToLower(raw.MatchType),
		strings.Join(ids, ","),
		raw.MatchTime,
	)
}