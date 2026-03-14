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

// sortedParticipantIDs returns sorted cagematch IDs as a joined string.
func sortedParticipantIDs(raw RawMatch) string {
	ids := make([]string, 0, len(raw.Participants))
	for _, p := range raw.Participants {
		if p.CagematchID > 0 {
			ids = append(ids, fmt.Sprintf("%d", p.CagematchID))
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// BuildMatchKey generates a deterministic composite key for dedup.
//
// Primary key (preferred): cm:EVENT_ID|sorted_participant_ids|match_time
//   Uses the Cagematch event ID — stable even when event names change
//   (e.g. "ROH Taping" → "ROH on HonorClub #157") or casing differs.
//   Participants + time distinguish multiple matches on the same event.
//
// Fallback key (no event ID):
//   date|event_name|match_type|sorted_participant_ids|match_time
func BuildMatchKey(raw RawMatch) string {
	pids := sortedParticipantIDs(raw)

	if raw.CagematchEventID > 0 {
		return fmt.Sprintf("cm:%d|%s|%s", raw.CagematchEventID, pids, raw.MatchTime)
	}

	return fmt.Sprintf("%s|%s|%s|%s|%s",
		raw.Date.Format("2006-01-02"),
		strings.ToLower(raw.EventName),
		strings.ToLower(raw.MatchType),
		pids,
		raw.MatchTime,
	)
}

// BuildMatchKeyNoTime generates the same key as BuildMatchKey but without the
// match_time component. Used for fuzzy dedup when Cagematch adds match_time
// after initial upload.
func BuildMatchKeyNoTime(raw RawMatch) string {
	pids := sortedParticipantIDs(raw)

	if raw.CagematchEventID > 0 {
		return fmt.Sprintf("cm:%d|%s", raw.CagematchEventID, pids)
	}

	return fmt.Sprintf("%s|%s|%s|%s",
		raw.Date.Format("2006-01-02"),
		strings.ToLower(raw.EventName),
		strings.ToLower(raw.MatchType),
		pids,
	)
}
