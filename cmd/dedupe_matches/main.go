// dedupe_matches finds and merges duplicate match rows created by key drift:
// participant sets that changed between scrapes (ghost-name parser changes,
// wrestlers gaining Cagematch links), match_time added after upload, and key
// format migrations. Two matches are duplicates when they share the event
// (same cagematch_event_id, or same date + event name), the same match type,
// and one participant-ID set contains the other (or the type is a multi-fall
// type like a battle royal, which is one entry per event+type by definition).
//
// The lowest-ID row is kept; missing participants and empty fields are moved
// onto it, then the duplicate row, its participants, and its ELO/momentum
// history are deleted. Run an ELO recalculation afterwards.
//
// Dry run (default):  go run ./cmd/dedupe_matches -db joshi.db
// Apply changes:      go run ./cmd/dedupe_matches -db joshi.db -apply
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"joshi-rankings-api/scraper"
)

type matchRow struct {
	ID               uint
	CagematchEventID int
	Date             time.Time
	EventName        string
	MatchType        string
	MatchTime        string
	FinishType       string
	Venue            string
	Location         string
	Stipulation      string
	MatchIndex       int
}

func main() {
	dbPath := flag.String("db", "joshi.db", "path to sqlite database")
	apply := flag.Bool("apply", false, "apply changes (default is dry run)")
	flag.Parse()

	db, err := gorm.Open(sqlite.Open(*dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		log.Fatalf("opening %s: %v", *dbPath, err)
	}

	mode := "DRY RUN"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("=== dedupe_matches (%s) on %s ===\n", mode, *dbPath)

	var matches []matchRow
	db.Raw(`SELECT id, cagematch_event_id, date, event_name, match_type,
	               match_time, finish_type, venue, location, stipulation, match_index
	        FROM matches ORDER BY id`).Scan(&matches)

	// One pass over match_participants → per-match Cagematch-ID sets
	type mpRow struct {
		MatchID uint
		CMID    int
	}
	var mps []mpRow
	db.Raw(`SELECT mp.match_id,
	               CASE WHEN mp.ghost_cagematch_id > 0 THEN mp.ghost_cagematch_id
	                    ELSE COALESCE(w.cagematch_id, 0) END AS cm_id
	        FROM match_participants mp
	        LEFT JOIN wrestlers w ON w.id = mp.wrestler_id`).Scan(&mps)

	cmSets := make(map[uint]map[int]bool)
	for _, mp := range mps {
		if mp.CMID <= 0 {
			continue
		}
		if cmSets[mp.MatchID] == nil {
			cmSets[mp.MatchID] = make(map[int]bool)
		}
		cmSets[mp.MatchID][mp.CMID] = true
	}

	// Group by date; compare within group. Kept rows are canonicals; every
	// later row that matches a canonical merges into it.
	byDate := make(map[string][]matchRow)
	for _, m := range matches {
		byDate[m.Date.Format("2006-01-02")] = append(byDate[m.Date.Format("2006-01-02")], m)
	}

	dupes := 0
	for _, group := range byDate {
		var canonicals []matchRow
		for _, m := range group {
			merged := false
			for i := range canonicals {
				c := &canonicals[i]
				sameEvent := (m.CagematchEventID > 0 && m.CagematchEventID == c.CagematchEventID) ||
					(m.EventName != "" && strings.EqualFold(m.EventName, c.EventName))
				if !sameEvent || !strings.EqualFold(m.MatchType, c.MatchType) {
					continue
				}
				if !scraper.IsMultiFallType(m.MatchType) && !scraper.CMIDSetsOverlap(cmSets[m.ID], cmSets[c.ID]) {
					continue
				}
				dupes++
				fmt.Printf("  dup #%d → keep #%d  %s | %s | %s (%d vs %d participants)\n",
					m.ID, c.ID, m.Date.Format("2006-01-02"), m.EventName, m.MatchType,
					len(cmSets[m.ID]), len(cmSets[c.ID]))
				if *apply {
					mergeInto(db, c, m, cmSets)
				}
				merged = true
				break
			}
			if !merged {
				canonicals = append(canonicals, m)
			}
		}
	}

	fmt.Printf("\n%d duplicate matches found.\n", dupes)
	if *apply && dupes > 0 {
		fmt.Println("Merged. Now run an ELO recalculation (POST /api/scraper/recalculate) — duplicate matches were counted in the ratings.")
	} else if dupes > 0 {
		fmt.Println("Dry run — re-run with -apply to merge.")
	}
}

// mergeInto moves dup's extra participants and missing fields onto keep, then
// deletes dup and its participant/history rows.
func mergeInto(db *gorm.DB, keep *matchRow, dup matchRow, cmSets map[uint]map[int]bool) {
	keepSet := cmSets[keep.ID]
	if keepSet == nil {
		keepSet = make(map[int]bool)
		cmSets[keep.ID] = keepSet
	}

	// Move participants keep doesn't have yet (by Cagematch ID)
	type mpRow struct {
		ID   uint
		CMID int
	}
	var dupMPs []mpRow
	db.Raw(`SELECT mp.id,
	               CASE WHEN mp.ghost_cagematch_id > 0 THEN mp.ghost_cagematch_id
	                    ELSE COALESCE(w.cagematch_id, 0) END AS cm_id
	        FROM match_participants mp
	        LEFT JOIN wrestlers w ON w.id = mp.wrestler_id
	        WHERE mp.match_id = ?`, dup.ID).Scan(&dupMPs)

	for _, mp := range dupMPs {
		if mp.CMID > 0 && !keepSet[mp.CMID] {
			db.Exec(`UPDATE match_participants SET match_id = ? WHERE id = ?`, keep.ID, mp.ID)
			keepSet[mp.CMID] = true
		}
	}

	// Fill empty fields on the kept row from the duplicate
	fills := map[string]interface{}{}
	if keep.MatchTime == "" && dup.MatchTime != "" {
		fills["match_time"] = dup.MatchTime
		keep.MatchTime = dup.MatchTime
	}
	if keep.FinishType == "" && dup.FinishType != "" {
		fills["finish_type"] = dup.FinishType
		keep.FinishType = dup.FinishType
	}
	if keep.Venue == "" && dup.Venue != "" {
		fills["venue"] = dup.Venue
		keep.Venue = dup.Venue
	}
	if keep.Location == "" && dup.Location != "" {
		fills["location"] = dup.Location
		keep.Location = dup.Location
	}
	if keep.Stipulation == "" && dup.Stipulation != "" {
		fills["stipulation"] = dup.Stipulation
		keep.Stipulation = dup.Stipulation
	}
	if keep.CagematchEventID == 0 && dup.CagematchEventID > 0 {
		fills["cagematch_event_id"] = dup.CagematchEventID
		keep.CagematchEventID = dup.CagematchEventID
	}
	if len(fills) > 0 {
		db.Table("matches").Where("id = ?", keep.ID).Updates(fills)
	}

	db.Exec(`DELETE FROM match_participants WHERE match_id = ?`, dup.ID)
	db.Exec(`DELETE FROM elo_histories WHERE match_id = ?`, dup.ID)
	db.Exec(`DELETE FROM momentum_histories WHERE match_id = ?`, dup.ID)
	db.Exec(`DELETE FROM matches WHERE id = ?`, dup.ID)
}
