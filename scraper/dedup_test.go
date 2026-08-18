package scraper

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"joshi-rankings-api/models"
)

// TestFindExistingMatchKeyDrift verifies a re-scrape still matches a stored
// match after its key drifted: a participant gained a Cagematch link and a
// match_time was added.
func TestFindExistingMatchKeyDrift(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&models.Wrestler{}, &models.Match{}, &models.MatchParticipant{})
	p := &Processor{db: db}

	w1 := models.Wrestler{Name: "Mayu Iwatani", CagematchID: 10}
	w2 := models.Wrestler{Name: "Syuri", CagematchID: 20}
	db.Create(&w1)
	db.Create(&w2)

	date := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	stored := RawMatch{
		MatchType: "Tag Team Match", EventName: "Stardom Show", Date: date,
		CagematchEventID: 555,
		Participants: []RawParticipant{
			{Name: "Mayu Iwatani", CagematchID: 10, Team: 1},
			{Name: "Syuri", CagematchID: 20, Team: 2},
			// partner had no Cagematch link at first scrape → ghost, no ID
		},
	}
	match := models.Match{
		MatchType: stored.MatchType, EventName: stored.EventName, Date: date,
		CagematchEventID: 555, MatchKey: BuildMatchKey(stored),
	}
	db.Create(&match)
	db.Create(&models.MatchParticipant{MatchID: match.ID, WrestlerID: w1.ID, Team: 1})
	db.Create(&models.MatchParticipant{MatchID: match.ID, WrestlerID: w2.ID, Team: 2})

	// Re-scrape: partner now linked (CM#30) and match_time added → different key
	rescrape := stored
	rescrape.MatchTime = "15:30"
	rescrape.Participants = append(rescrape.Participants, RawParticipant{Name: "AZM", CagematchID: 30, Team: 1})
	if BuildMatchKey(rescrape) == BuildMatchKey(stored) {
		t.Fatal("test setup broken: keys should differ")
	}

	if got := p.FindExistingMatch(rescrape); got != match.ID {
		t.Errorf("drifted re-scrape: got match ID %d, want %d", got, match.ID)
	}

	// Same event, disjoint participants → different match, must NOT dedup
	other := RawMatch{
		MatchType: "Singles Match", EventName: "Stardom Show", Date: date,
		CagematchEventID: 555,
		Participants: []RawParticipant{
			{Name: "A", CagematchID: 40}, {Name: "B", CagematchID: 50},
		},
	}
	if got := p.FindExistingMatch(other); got != 0 {
		t.Errorf("distinct match wrongly deduped into %d", got)
	}
}
