package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// YearELOLeader represents a wrestler's ELO at year end
type YearELOLeader struct {
	Year       int     `json:"year"`
	WrestlerID uint    `json:"wrestler_id"`
	Name       string  `json:"name"`
	ELO        float64 `json:"elo"`
	Rank       int     `json:"rank"`
}

// YearStats represents aggregate stats for a year
type YearStats struct {
	Year          int `json:"year"`
	Matches       int `json:"matches"`
	ActiveWrestlers int `json:"active_wrestlers"`
	Debuts        int `json:"debuts"`
	TitleMatches  int `json:"title_matches"`
}

// GET /api/timeline/elo-leaders?top=10&from=1970&to=2025
// Returns top N wrestlers' ELO at the end of each year
func GetELOLeaders(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		top := 10
		if t, err := strconv.Atoi(c.Query("top")); err == nil && t > 0 && t <= 50 {
			top = t
		}
		fromYear := 1970
		if f, err := strconv.Atoi(c.Query("from")); err == nil {
			fromYear = f
		}
		toYear := 2025
		if t, err := strconv.Atoi(c.Query("to")); err == nil {
			toYear = t
		}

		// For each year, get the last ELO entry per wrestler, then rank
		// Using a raw query for performance on 1.7M rows
		var results []YearELOLeader

		// Step 1: Get year-end ELOs and ranks for all wrestlers
		// Step 2: Find wrestlers who were top N in at least one year
		// Step 3: Return ALL year-end data for those wrestlers (so lines are continuous)
		query := `
			WITH year_end AS (
				SELECT 
					eh.wrestler_id,
					CAST(strftime('%Y', eh.match_date) AS INTEGER) as yr,
					eh.elo,
					ROW_NUMBER() OVER (
						PARTITION BY eh.wrestler_id, strftime('%Y', eh.match_date) 
						ORDER BY eh.match_date DESC, eh.id DESC
					) as rn
				FROM elo_histories eh
				WHERE eh.match_date IS NOT NULL
				AND CAST(strftime('%Y', eh.match_date) AS INTEGER) BETWEEN ? AND ?
			),
			yearly AS (
				SELECT yr, wrestler_id, elo,
					ROW_NUMBER() OVER (PARTITION BY yr ORDER BY elo DESC) as rank
				FROM year_end
				WHERE rn = 1
			),
			notable AS (
				SELECT DISTINCT wrestler_id
				FROM yearly
				WHERE rank <= ?
			)
			SELECT y.yr as year, y.wrestler_id, w.name, y.elo, y.rank
			FROM yearly y
			JOIN wrestlers w ON w.id = y.wrestler_id
			WHERE y.wrestler_id IN (SELECT wrestler_id FROM notable)
			ORDER BY y.yr, y.rank
		`

		db.Raw(query, fromYear, toYear, top).Scan(&results)
		c.JSON(http.StatusOK, results)
	}
}

// GET /api/timeline/era-stats?from=1950&to=2025
// Returns yearly aggregate stats
func GetEraStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		fromYear := 1950
		if f, err := strconv.Atoi(c.Query("from")); err == nil {
			fromYear = f
		}
		toYear := 2025
		if t, err := strconv.Atoi(c.Query("to")); err == nil {
			toYear = t
		}

		var stats []YearStats

		query := `
			SELECT 
				CAST(strftime('%Y', m.date) AS INTEGER) as year,
				COUNT(DISTINCT m.id) as matches,
				COUNT(DISTINCT mp.wrestler_id) as active_wrestlers,
				(SELECT COUNT(*) FROM wrestlers w2 WHERE w2.debut_year = CAST(strftime('%Y', m.date) AS INTEGER)) as debuts,
				SUM(CASE WHEN m.is_title_match = 1 THEN 1 ELSE 0 END) as title_matches
			FROM matches m
			JOIN match_participants mp ON mp.match_id = m.id
			WHERE CAST(strftime('%Y', m.date) AS INTEGER) BETWEEN ? AND ?
			GROUP BY CAST(strftime('%Y', m.date) AS INTEGER)
			ORDER BY year
		`

		db.Raw(query, fromYear, toYear).Scan(&stats)
		c.JSON(http.StatusOK, stats)
	}
}

// ELOSnapshot represents a wrestler's ELO at a specific point
type ELOSnapshot struct {
	WrestlerID uint    `json:"wrestler_id"`
	Name       string  `json:"name"`
	ELO        float64 `json:"elo"`
	Momentum   float64 `json:"momentum"`
	Matches    int     `json:"matches"`
	Promotion  string  `json:"promotion"`
	Wins       int     `json:"wins"`
	Losses     int     `json:"losses"`
	Draws      int     `json:"draws"`
}

// GET /api/timeline/snapshot?year=1995&month=12&top=20
// Returns top wrestlers' ELO at a specific point in time
func GetELOSnapshot(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		year, err := strconv.Atoi(c.Query("year"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "year parameter required"})
			return
		}
		month := 12
		if m, err := strconv.Atoi(c.Query("month")); err == nil && m >= 1 && m <= 12 {
			month = m
		}
		top := 20
		if t, err := strconv.Atoi(c.Query("top")); err == nil && t > 0 && t <= 100 {
			top = t
		}

		var results []ELOSnapshot

		// Get last ELO entry per wrestler before the given date
		// Only include wrestlers who had at least one match in the selected year
		// Also calculate W/L/D record up to that point
		cutoff := fmt.Sprintf("%04d-%02d-31", year, month)
		query := `
			WITH active AS (
				SELECT DISTINCT eh.wrestler_id
				FROM elo_histories eh
				WHERE CAST(strftime('%Y', eh.match_date) AS INTEGER) = ?
				AND eh.match_date IS NOT NULL
			),
			latest AS (
				SELECT 
					eh.wrestler_id,
					eh.elo,
					ROW_NUMBER() OVER (PARTITION BY eh.wrestler_id ORDER BY eh.match_date DESC, eh.id DESC) as rn,
					COUNT(*) OVER (PARTITION BY eh.wrestler_id) as match_count
				FROM elo_histories eh
				WHERE eh.wrestler_id IN (SELECT wrestler_id FROM active)
				AND eh.match_date <= ?
				AND eh.match_date IS NOT NULL
			),
			records AS (
				SELECT 
					mp.wrestler_id,
					SUM(CASE WHEN mp.is_winner = 1 AND m.is_draw = 0 THEN 1 ELSE 0 END) as wins,
					SUM(CASE WHEN mp.is_winner = 0 AND m.is_draw = 0 THEN 1 ELSE 0 END) as losses,
					SUM(CASE WHEN m.is_draw = 1 THEN 1 ELSE 0 END) as draws
				FROM match_participants mp
				JOIN matches m ON m.id = mp.match_id
				WHERE mp.wrestler_id IN (SELECT wrestler_id FROM active)
				AND mp.wrestler_id > 0
				AND m.date <= ?
				GROUP BY mp.wrestler_id
			),
			latest_momentum AS (
				SELECT 
					mh.wrestler_id,
					mh.momentum,
					ROW_NUMBER() OVER (PARTITION BY mh.wrestler_id ORDER BY mh.match_date DESC, mh.id DESC) as rn
				FROM momentum_histories mh
				WHERE mh.wrestler_id IN (SELECT wrestler_id FROM active)
				AND mh.match_date <= ?
				AND mh.match_date IS NOT NULL
			)
			SELECT l.wrestler_id, w.name, l.elo, 
				COALESCE(lm.momentum, 0) as momentum,
				l.match_count as matches, 
				COALESCE(w.promotion, 'Freelance') as promotion,
				COALESCE(r.wins, 0) as wins, 
				COALESCE(r.losses, 0) as losses, 
				COALESCE(r.draws, 0) as draws
			FROM latest l
			JOIN wrestlers w ON w.id = l.wrestler_id
			LEFT JOIN records r ON r.wrestler_id = l.wrestler_id
			LEFT JOIN latest_momentum lm ON lm.wrestler_id = l.wrestler_id AND lm.rn = 1
			WHERE l.rn = 1
			ORDER BY l.elo DESC
			LIMIT ?
		`

		db.Raw(query, year, cutoff, cutoff, cutoff, top).Scan(&results)
		c.JSON(http.StatusOK, results)
	}
}
