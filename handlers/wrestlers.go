package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

// GET /api/wrestler-names — slim list for search autocomplete (id, name, promotion only)
// Cached at edge (Cloudflare) for 5 minutes to avoid repeated downloads
func GetWrestlerNames(db *gorm.DB) gin.HandlerFunc {
	// In-memory cache so we don't hit the DB on every request either
	var cached []byte
	var cachedAt time.Time
	var cacheMu sync.Mutex
	cacheTTL := 5 * time.Minute

	return func(c *gin.Context) {
		cacheMu.Lock()
		if cached != nil && time.Since(cachedAt) < cacheTTL {
			data := cached
			cacheMu.Unlock()
			c.Header("Cache-Control", "public, max-age=300")
			c.Data(http.StatusOK, "application/json; charset=utf-8", data)
			return
		}
		cacheMu.Unlock()

		type WrestlerName struct {
			ID        uint   `json:"id"`
			Name      string `json:"name"`
			Promotion string `json:"promotion"`
		}
		var names []WrestlerName
		if err := db.Model(&models.Wrestler{}).Select("id, name, promotion").Find(&names).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve wrestlers"})
			return
		}

		data, _ := json.Marshal(names)
		cacheMu.Lock()
		cached = data
		cachedAt = time.Now()
		cacheMu.Unlock()

		c.Header("Cache-Control", "public, max-age=300")
		c.Data(http.StatusOK, "application/json; charset=utf-8", data)
	}
}

// GET /api/wrestler-slim — id, name, promotion, elo (for ranking calc without full payload)
func GetWrestlerSlim(db *gorm.DB) gin.HandlerFunc {
	var cached []byte
	var cachedAt time.Time
	var cacheMu sync.Mutex
	cacheTTL := 5 * time.Minute

	return func(c *gin.Context) {
		cacheMu.Lock()
		if cached != nil && time.Since(cachedAt) < cacheTTL {
			data := cached
			cacheMu.Unlock()
			c.Header("Cache-Control", "public, max-age=300")
			c.Data(http.StatusOK, "application/json; charset=utf-8", data)
			return
		}
		cacheMu.Unlock()

		type WrestlerSlim struct {
			ID        uint    `json:"id"`
			Name      string  `json:"name"`
			Promotion string  `json:"promotion"`
			ELO       float64 `json:"elo"`
		}
		var slim []WrestlerSlim
		if err := db.Model(&models.Wrestler{}).Select("id, name, promotion, elo").Find(&slim).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve wrestlers"})
			return
		}

		data, _ := json.Marshal(slim)
		cacheMu.Lock()
		cached = data
		cachedAt = time.Now()
		cacheMu.Unlock()

		c.Header("Cache-Control", "public, max-age=300")
		c.Data(http.StatusOK, "application/json; charset=utf-8", data)
	}
}

func GetWrestlers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var wrestlers []models.Wrestler
		query := db.Preload("Aliases")

		if promotion := c.Query("promotion"); promotion != "" {
			query = query.Where("promotion = ?", promotion)
		}
		if err := query.Find(&wrestlers).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve wrestlers"})
			return
		}
		c.JSON(http.StatusOK, wrestlers)
	}
}

func GetWrestler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		var wrestler models.Wrestler
		if err := db.Preload("Aliases").Preload("Socials").First(&wrestler, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wrestler not found"})
			return
		}
		c.JSON(http.StatusOK, wrestler)
	}
}

// GET /api/wrestlers/:id/promotions
func GetWrestlerPromotions(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		var history []models.PromotionHistory
		db.Where("wrestler_id = ?", id).Order("year DESC, matches DESC").Find(&history)
		c.JSON(http.StatusOK, history)
	}
}

// GET /api/promotions — all scraped promotions
func GetPromotions(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var promos []models.Promotion
		query := db.Order("name ASC")
		if country := c.Query("country"); country != "" {
			query = query.Where("country = ?", country)
		}
		if region := c.Query("region"); region != "" {
			query = query.Where("region = ?", region)
		}
		query.Find(&promos)
		c.JSON(http.StatusOK, promos)
	}
}

// GET /api/promotions/roster?promotion=World Wonder Ring Stardom&year=2023
func GetPromotionRoster(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		promotion := c.Query("promotion")
		year := c.Query("year")
		if promotion == "" || year == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "promotion and year required"})
			return
		}

		type RosterEntry struct {
			WrestlerID uint    `json:"wrestler_id"`
			Name       string  `json:"name"`
			Matches    int     `json:"matches"`
			ELO        float64 `json:"elo"`
			Promotion  string  `json:"current_promotion"`
		}

		var roster []RosterEntry
		db.Raw(`
			SELECT ph.wrestler_id, w.name, ph.matches, w.elo, w.promotion
			FROM promotion_histories ph
			JOIN wrestlers w ON w.id = ph.wrestler_id
			WHERE ph.promotion = ? AND ph.year = ?
			ORDER BY ph.matches DESC
		`, promotion, year).Scan(&roster)

		c.JSON(http.StatusOK, roster)
	}
}

// GET /api/promotions/list
func GetPromotionList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		type PromoInfo struct {
			Promotion string `json:"promotion"`
			Wrestlers int    `json:"wrestlers"`
			YearFrom  int    `json:"year_from"`
			YearTo    int    `json:"year_to"`
		}
		var promos []PromoInfo
		db.Raw(`
			SELECT promotion, COUNT(DISTINCT wrestler_id) as wrestlers, 
			       MIN(year) as year_from, MAX(year) as year_to
			FROM promotion_histories
			GROUP BY promotion
			ORDER BY wrestlers DESC
		`).Scan(&promos)
		c.JSON(http.StatusOK, promos)
	}
}

// GET /api/wrestlers/:id/titles
func GetWrestlerTitles(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}
		var reigns []models.TitleReign
		db.Where("wrestler_id = ?", id).Order("won_date DESC").Find(&reigns)
		c.JSON(http.StatusOK, reigns)
	}
}

// GET /api/titles — list all titles with reign counts
func GetTitles(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		type TitleInfo struct {
			TitleName        string `json:"title_name"`
			CagematchTitleID int    `json:"cagematch_title_id"`
			TotalReigns      int    `json:"total_reigns"`
			TrackedReigns    int    `json:"tracked_reigns"`
			UntrackedReigns  int    `json:"untracked_reigns"`
			FirstReign       string `json:"first_reign"`
			LatestReign      string `json:"latest_reign"`
			CurrentHolder    string `json:"current_holder"`
			Promotion        string `json:"promotion"`
			Status           string `json:"status"`
		}
		var titles []TitleInfo
		db.Raw(`
			SELECT tr.title_name, tr.cagematch_title_id, 
			       COUNT(*) as total_reigns,
			       SUM(CASE WHEN tr.untracked = 0 OR tr.untracked IS NULL THEN 1 ELSE 0 END) as tracked_reigns,
			       SUM(CASE WHEN tr.untracked = 1 THEN 1 ELSE 0 END) as untracked_reigns,
			       MIN(tr.won_date) as first_reign,
			       MAX(tr.won_date) as latest_reign,
			       COALESCE(t.promotion, '') as promotion,
			       COALESCE(t.status, '') as status
			FROM title_reigns tr
			LEFT JOIN titles t ON t.cagematch_id = tr.cagematch_title_id
			GROUP BY tr.cagematch_title_id
			ORDER BY total_reigns DESC
		`).Scan(&titles)

		// Get current holders for each title (latest reign with no lost_date — tag titles have multiple)
		for i, t := range titles {
			var holders []struct {
				Name string
			}
			db.Raw(`
				SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as name
				FROM title_reigns tr
				LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
				WHERE tr.cagematch_title_id = ? AND tr.lost_date IS NULL
				ORDER BY tr.won_date DESC
			`, t.CagematchTitleID).Scan(&holders)
			names := make([]string, len(holders))
			for j, h := range holders {
				names[j] = h.Name
			}
			titles[i].CurrentHolder = strings.Join(names, " & ")
		}

		c.JSON(http.StatusOK, titles)
	}
}

// GET /api/titles/:id/elo-history — title ELO chart (holder's ELO over time)
func GetTitleELOHistory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		titleID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid title ID"})
			return
		}

		type TitleELOPoint struct {
			Date         string  `json:"date"`
			WrestlerName string  `json:"wrestler_name"`
			WrestlerID   uint    `json:"wrestler_id"`
			ELO          float64 `json:"elo"`
			ReignNumber  int     `json:"reign_number"`
		}

		// For each reign, get the average ELO of all tracked holders at the time they won.
		// This prevents zigzag lines on tag titles by combining partners into one point.
		var points []TitleELOPoint
		db.Raw(`
			SELECT 
				tr.won_date as date,
				GROUP_CONCAT(w.name, ' & ') as wrestler_name,
				MIN(w.id) as wrestler_id,
				AVG(COALESCE(
					(SELECT eh.elo FROM elo_histories eh 
					 WHERE eh.wrestler_id = tr.wrestler_id 
					 AND eh.match_date <= tr.won_date 
					 ORDER BY eh.match_date DESC, eh.id DESC LIMIT 1),
					w.elo
				)) as elo,
				tr.reign_number
			FROM title_reigns tr
			JOIN wrestlers w ON w.id = tr.wrestler_id
			WHERE tr.cagematch_title_id = ?
			GROUP BY tr.reign_number, tr.won_date
			ORDER BY tr.won_date ASC
		`, titleID).Scan(&points)

		c.JSON(http.StatusOK, points)
	}
}

// ELOHistoryPoint is the enriched response for elo-history endpoints.
type ELOHistoryPoint struct {
	ID         uint      `json:"id"`
	WrestlerID uint      `json:"wrestler_id"`
	ELO        float64   `json:"elo"`
	MatchID    uint      `json:"match_id"`
	MatchDate  time.Time `json:"match_date"`
	EventName  string    `json:"event_name"`
	Opponents  string    `json:"opponents"`
	Result     string    `json:"result"`
	EloChange  float64   `json:"elo_change"`
}

func fetchEnrichedELOHistory(db *gorm.DB, wrestlerID int) []ELOHistoryPoint {
	var points []ELOHistoryPoint
	db.Raw(`
		SELECT
			eh.id, eh.wrestler_id, eh.elo, eh.match_id, eh.match_date,
			m.event_name,
			CASE WHEN m.is_draw THEN 'D'
			     WHEN mp_self.is_winner THEN 'W'
			     ELSE 'L' END as result,
			mp_self.elo_change
		FROM elo_histories eh
		LEFT JOIN matches m ON m.id = eh.match_id
		LEFT JOIN match_participants mp_self ON mp_self.match_id = eh.match_id AND mp_self.wrestler_id = eh.wrestler_id
		WHERE eh.wrestler_id = ?
		ORDER BY eh.match_date ASC, eh.id ASC
	`, wrestlerID).Scan(&points)

	// Build a map of match_id -> opponent names for all matches in this history
	if len(points) == 0 {
		return points
	}
	matchIDs := make([]uint, len(points))
	for i, p := range points {
		matchIDs[i] = p.MatchID
	}

	type OppRow struct {
		MatchID      uint
		WrestlerName string
		GhostName    string
	}
	var oppRows []OppRow
	db.Raw(`
		SELECT mp.match_id, COALESCE(w.name, '') as wrestler_name, COALESCE(mp.ghost_name, '') as ghost_name
		FROM match_participants mp
		LEFT JOIN wrestlers w ON w.id = mp.wrestler_id
		WHERE mp.match_id IN ? AND mp.wrestler_id != ?
	`, matchIDs, wrestlerID).Scan(&oppRows)

	oppMap := make(map[uint][]string)
	for _, r := range oppRows {
		name := r.WrestlerName
		if name == "" {
			name = r.GhostName
		}
		if name != "" {
			oppMap[r.MatchID] = append(oppMap[r.MatchID], name)
		}
	}

	for i := range points {
		if names, ok := oppMap[points[i].MatchID]; ok {
			points[i].Opponents = strings.Join(names, ", ")
		}
	}

	return points
}

// GET /api/wrestlers/:id/elo-history?limit=500
func GetWrestlerELOHistory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		points := fetchEnrichedELOHistory(db, id)
		c.JSON(http.StatusOK, points)
	}
}

// GET /api/elo-history?ids=1,5,12&limit=500
func GetMultiELOHistory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		idsParam := c.Query("ids")
		if idsParam == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ids parameter required"})
			return
		}

		// Parse IDs
		var ids []uint
		for _, s := range splitIDs(idsParam) {
			if n, err := strconv.Atoi(s); err == nil {
				ids = append(ids, uint(n))
			}
		}

		// Fetch enriched history for each wrestler
		result := make(map[uint][]ELOHistoryPoint)
		for _, id := range ids {
			result[id] = fetchEnrichedELOHistory(db, int(id))
		}

		c.JSON(http.StatusOK, result)
	}
}

func splitIDs(s string) []string {
	var parts []string
	current := ""
	for _, c := range s {
		if c == ',' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func CreateWrestler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var wrestler models.Wrestler
		if err := c.ShouldBindJSON(&wrestler); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		
		wrestler.ELO = 1200 // default ELO

		if err := db.Create(&wrestler).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create wrestler"})
			return
		}
		c.JSON(http.StatusCreated, wrestler)
	}
}

func UpdateWrestler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		var wrestler models.Wrestler
		if err := db.First(&wrestler, id).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Wrestler not found"})
			return
		}
			
		var input struct {
			Name      string `json:"name"`
			Promotion string `json:"promotion"`
			ImageURL  string `json:"image_url"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		db.Model(&wrestler).Updates(input)
		c.JSON(http.StatusOK, wrestler)
	}
}

// GET /api/wrestlers/:id/stats — streaks + notable opponents
func GetWrestlerStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		// Get all match results in chronological order for streak calculation
		type matchResult struct {
			IsWinner bool
			IsDraw   bool
		}
		var results []matchResult
		db.Raw(`
			SELECT mp.is_winner, m.is_draw
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			WHERE mp.wrestler_id = ?
			ORDER BY m.date ASC, m.id ASC
		`, id).Scan(&results)

		// Calculate longest winning and losing streaks
		longestWin, longestLoss := 0, 0
		currentWin, currentLoss := 0, 0
		for _, r := range results {
			if r.IsDraw {
				currentWin = 0
				currentLoss = 0
			} else if r.IsWinner {
				currentWin++
				currentLoss = 0
				if currentWin > longestWin {
					longestWin = currentWin
				}
			} else {
				currentLoss++
				currentWin = 0
				if currentLoss > longestLoss {
					longestLoss = currentLoss
				}
			}
		}

		// Notable opponents — top 5 ELO opponents in singles matches
		type opponent struct {
			WrestlerID uint    `json:"wrestler_id"`
			Name       string  `json:"name"`
			ELO        float64 `json:"elo"`
			Promotion  string  `json:"promotion"`
			Wins       int     `json:"wins"`
			Losses     int     `json:"losses"`
			Draws      int     `json:"draws"`
		}
		var singlesOpponents []opponent
		db.Raw(`
			SELECT w.id as wrestler_id, w.name, w.elo, w.promotion,
				SUM(CASE WHEN mp_self.is_winner = 1 AND m.is_draw = 0 THEN 1 ELSE 0 END) as wins,
				SUM(CASE WHEN mp_self.is_winner = 0 AND m.is_draw = 0 THEN 1 ELSE 0 END) as losses,
				SUM(CASE WHEN m.is_draw = 1 THEN 1 ELSE 0 END) as draws
			FROM match_participants mp_opp
			JOIN matches m ON m.id = mp_opp.match_id
			JOIN wrestlers w ON w.id = mp_opp.wrestler_id
			JOIN match_participants mp_self ON mp_self.match_id = m.id AND mp_self.wrestler_id = ?
			WHERE mp_opp.wrestler_id != ? AND mp_opp.wrestler_id > 0
			AND (SELECT COUNT(*) FROM match_participants mp2 WHERE mp2.match_id = m.id) <= 2
			GROUP BY mp_opp.wrestler_id
			ORDER BY w.elo DESC
			LIMIT 5
		`, id, id).Scan(&singlesOpponents)

		// Top 5 ELO opponents across ALL match types
		var allOpponents []opponent
		db.Raw(`
			SELECT w.id as wrestler_id, w.name, w.elo, w.promotion,
				SUM(CASE WHEN mp_self.is_winner = 1 AND m.is_draw = 0 THEN 1 ELSE 0 END) as wins,
				SUM(CASE WHEN mp_self.is_winner = 0 AND m.is_draw = 0 THEN 1 ELSE 0 END) as losses,
				SUM(CASE WHEN m.is_draw = 1 THEN 1 ELSE 0 END) as draws
			FROM match_participants mp_opp
			JOIN matches m ON m.id = mp_opp.match_id
			JOIN wrestlers w ON w.id = mp_opp.wrestler_id
			JOIN match_participants mp_self ON mp_self.match_id = m.id AND mp_self.wrestler_id = ?
			WHERE mp_opp.wrestler_id != ? AND mp_opp.wrestler_id > 0
			GROUP BY mp_opp.wrestler_id
			ORDER BY w.elo DESC
			LIMIT 5
		`, id, id).Scan(&allOpponents)

		c.JSON(http.StatusOK, gin.H{
			"longest_win_streak":  longestWin,
			"longest_loss_streak": longestLoss,
			"current_win_streak":  currentWin,
			"current_loss_streak": currentLoss,
			"top_singles_opponents": singlesOpponents,
			"top_all_opponents":     allOpponents,
		})
	}
}

func DeleteWrestler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}

		if err := db.Delete(&models.Wrestler{}, id).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete wrestler"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Wrestler deleted"})
	}
}