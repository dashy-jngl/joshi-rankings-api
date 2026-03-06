package database

import (
	"encoding/json"
	"log"
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

func InitDB(dbPath string) (*gorm.DB, error) {
	//open DB
	// Enable WAL mode + busy timeout so reads work during writes
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=10000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	//Auto-Migrate - create tables for structs
	db.AutoMigrate(&models.Wrestler{}, &models.WrestlerAlias{}, &models.Socials{}, &models.Match{}, &models.MatchParticipant{}, &models.ELOHistory{}, &models.MomentumHistory{}, &models.SkippedWrestler{}, &models.PromotionHistory{}, &models.TitleReign{}, &models.Promotion{}, &models.Title{}, &models.User{}, &models.Setting{})

	// Performance indexes (IF NOT EXISTS — safe to re-run)
	indexes := []string{
		// match_participants: the most joined table — composite indexes for common query patterns
		`CREATE INDEX IF NOT EXISTS idx_mp_match_wrestler ON match_participants(match_id, wrestler_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mp_wrestler_match ON match_participants(wrestler_id, match_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mp_wrestler_winner ON match_participants(wrestler_id, is_winner)`,
		`CREATE INDEX IF NOT EXISTS idx_mp_elo_change ON match_participants(wrestler_id, elo_change)`,

		// matches: date filtering, title matches, finish types
		`CREATE INDEX IF NOT EXISTS idx_matches_date ON matches(date)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_title ON matches(is_title_match)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_date_title ON matches(date, is_title_match)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_draw ON matches(is_draw)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_finish ON matches(finish_type)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_event_id ON matches(cagematch_event_id)`,

		// wrestlers: promotion filtering, sorting by ELO
		`CREATE INDEX IF NOT EXISTS idx_wrestlers_promotion ON wrestlers(promotion)`,
		`CREATE INDEX IF NOT EXISTS idx_wrestlers_elo ON wrestlers(elo)`,
		`CREATE INDEX IF NOT EXISTS idx_wrestlers_debut ON wrestlers(debut_year)`,
		`CREATE INDEX IF NOT EXISTS idx_wrestlers_match_count ON wrestlers(match_count)`,

		// elo_histories: wrestler timeline queries
		`CREATE INDEX IF NOT EXISTS idx_elo_hist_wrestler_date ON elo_histories(wrestler_id, match_date)`,

		// title_reigns: recent title changes, wrestler title history
		`CREATE INDEX IF NOT EXISTS idx_title_reigns_won ON title_reigns(won_date)`,
		`CREATE INDEX IF NOT EXISTS idx_title_reigns_wrestler ON title_reigns(wrestler_id, won_date)`,
		`CREATE INDEX IF NOT EXISTS idx_title_reigns_title ON title_reigns(cagematch_title_id)`,

		// promotion_histories: promotion page queries
		`CREATE INDEX IF NOT EXISTS idx_promo_hist_promotion ON promotion_histories(promotion)`,

		// network queries: opponent pair lookups
		`CREATE INDEX IF NOT EXISTS idx_mp_wrestler_team ON match_participants(wrestler_id, team)`,
	}
	for _, idx := range indexes {
		if err := db.Exec(idx).Error; err != nil {
			log.Printf("Index warning: %v", err)
		}
	}

	//seed if empty
	seedWrestlers(db)

	return db, nil
}

type seedData struct {
	Name        string   `json:"name"`
	Promotion   string   `json:"promotion"`
	CagematchID uint     `json:"cagematch_id"`
	Aliases     []string `json:"aliases"`
}

func seedWrestlers(db *gorm.DB) {
	// Skip if wrestlers already exist
	var count int64
	db.Model(&models.Wrestler{}).Count(&count)
	if count > 0 {
		return
	}

	// Read seed file
	file, err := os.ReadFile("seeds/wrestlers.json")
	if err != nil {
		log.Printf("No seed file found: %v", err)
		return
	}

	// Parse JSON
	var seeds []seedData
	json.Unmarshal(file, &seeds)

	// Create each wrestler
	for _, s := range seeds {
		wrestler := models.Wrestler{
			Name:        s.Name,
			Promotion:   s.Promotion,
			CagematchID: s.CagematchID,
		}
		db.Create(&wrestler)
		// Create aliases
		for _, alias := range s.Aliases {
			db.Create(&models.WrestlerAlias{
				WrestlerID: wrestler.ID,
				Alias:      alias,
			})
		}
	}

	log.Printf("Seeded %d wrestlers", len(seeds))
}
