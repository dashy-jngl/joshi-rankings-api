// cleanup_junk repairs match data corrupted by Cagematch's anti-scraping
// injections (hidden decoy numbers, zero-width characters) and by the old
// ghost-participant parser (junk names like ") (8:35)" or "Time Limit Draw (20:20)").
//
// Dry run (default):  go run ./cmd/cleanup_junk -db joshi.db
// Apply changes:      go run ./cmd/cleanup_junk -db joshi.db -apply
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"joshi-rankings-api/scraper"
)

func main() {
	dbPath := flag.String("db", "joshi.db", "path to sqlite database")
	apply := flag.Bool("apply", false, "apply changes (default is dry run)")
	limit := flag.Int("limit", 20, "max example rows to print per category")
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
	fmt.Printf("=== cleanup_junk (%s) on %s ===\n", mode, *dbPath)

	cleanGhostParticipants(db, *apply, *limit)
	cleanTextColumns(db, *apply, *limit)
}

// cleanGhostParticipants re-validates every stored ghost name with the same
// rules the scraper now uses. Junk rows are deleted, salvageable names are
// rewritten, and names that were glued together by zero-width spaces
// ("Fukigen Death, Kaori Yoneyama") are split into separate rows.
func cleanGhostParticipants(db *gorm.DB, apply bool, limit int) {
	type ghostRow struct {
		ID       uint
		MatchID  uint
		Team     int
		IsWinner bool
		Name     string
	}
	var rows []ghostRow
	db.Raw(`SELECT id, match_id, team, is_winner, ghost_name AS name
	        FROM match_participants
	        WHERE (wrestler_id = 0 OR wrestler_id IS NULL) AND ghost_name != ''`).Scan(&rows)

	deleted, updated, split := 0, 0, 0
	for _, row := range rows {
		// Names glued by zero-width chars may hold several wrestlers — split first
		var parts []string
		for _, seg := range strings.Split(scraper.CleanStoredText(row.Name), ", ") {
			parts = append(parts, scraper.SplitGhostSegment(seg)...)
		}

		switch {
		case len(parts) == 0:
			deleted++
			if deleted <= limit {
				fmt.Printf("  delete ghost %q (match %d)\n", row.Name, row.MatchID)
			}
			if apply {
				db.Exec(`DELETE FROM match_participants WHERE id = ?`, row.ID)
			}
		case len(parts) == 1 && parts[0] == row.Name:
			// already clean
		default:
			updated++
			if updated <= limit {
				fmt.Printf("  rewrite ghost %q → %v (match %d)\n", row.Name, parts, row.MatchID)
			}
			if apply {
				db.Exec(`UPDATE match_participants SET ghost_name = ? WHERE id = ?`, parts[0], row.ID)
				for _, extra := range parts[1:] {
					var count int64
					db.Raw(`SELECT COUNT(*) FROM match_participants WHERE match_id = ? AND ghost_name = ?`,
						row.MatchID, extra).Scan(&count)
					if count == 0 {
						db.Exec(`INSERT INTO match_participants (match_id, wrestler_id, team, is_winner, elo_change, ghost_name, ghost_cagematch_id)
						         VALUES (?, 0, ?, ?, 0, ?, 0)`, row.MatchID, row.Team, row.IsWinner, extra)
						split++
					}
				}
			} else if len(parts) > 1 {
				split += len(parts) - 1
			}
		}
	}
	fmt.Printf("ghost participants: %d junk deleted, %d rewritten, %d split-out rows added (of %d ghosts)\n",
		deleted, updated, split, len(rows))
}

// cleanTextColumns strips invisible characters and glued decoy numbers from
// all scraped text columns.
func cleanTextColumns(db *gorm.DB, apply bool, limit int) {
	targets := []struct {
		table   string
		columns []string
	}{
		{"matches", []string{"event_name", "venue", "location", "promotion", "stipulation"}},
		{"wrestlers", []string{"name", "birthplace", "promotion", "wrestling_style"}},
		{"wrestler_aliases", []string{"alias"}},
		{"promotions", []string{"name", "abbreviation", "location", "country"}},
		{"promotion_histories", []string{"promotion"}},
		{"titles", []string{"name", "promotion"}},
		{"title_reigns", []string{"title_name", "holder_name"}},
	}

	for _, tgt := range targets {
		for _, col := range tgt.columns {
			type textRow struct {
				ID  uint
				Val string
			}
			var rows []textRow
			db.Raw(fmt.Sprintf(`SELECT id, %s AS val FROM %s WHERE %s != ''`, col, tgt.table, col)).Scan(&rows)

			changed := 0
			for _, row := range rows {
				cleaned := scraper.CleanStoredText(row.Val)
				if cleaned == row.Val {
					continue
				}
				changed++
				if changed <= limit {
					fmt.Printf("  %s.%s [%d]: %q → %q\n", tgt.table, col, row.ID, row.Val, cleaned)
				}
				if apply {
					db.Exec(fmt.Sprintf(`UPDATE %s SET %s = ? WHERE id = ?`, tgt.table, col), cleaned, row.ID)
				}
			}
			if changed > 0 {
				fmt.Printf("%s.%s: %d rows cleaned\n", tgt.table, col, changed)
			}
		}
	}
}
