package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var (
	recordsCache     *StatsResponse
	recordsCacheMu   sync.RWMutex
	recordsCacheTime time.Time
	recordsCacheTTL  = 24 * time.Hour // effectively infinite — invalidated by scrape/recalc
)

// StreakEntry represents a wrestler's streak record
type StreakEntry struct {
	WrestlerID uint   `json:"wrestler_id"`
	Name       string `json:"name"`
	Promotion  string `json:"promotion"`
	Streak     int    `json:"streak"`
}

// FinishEntry represents a wrestler's finish type count
type FinishEntry struct {
	WrestlerID uint   `json:"wrestler_id"`
	Name       string `json:"name"`
	Promotion  string `json:"promotion"`
	Count      int    `json:"count"`
}

// StatsResponse contains various record leaderboards
type StatsResponse struct {
	LongestWinStreak  []StreakEntry  `json:"longest_win_streak"`
	LongestLoseStreak []StreakEntry  `json:"longest_lose_streak"`
	MostKOWins        []FinishEntry `json:"most_ko_wins"`
	MostCountOuts     []FinishEntry `json:"most_count_outs"`
	MostDQWins        []FinishEntry `json:"most_dq_wins"`
	MostDQLosses      []FinishEntry `json:"most_dq_losses"`
	MostTitleMatches  []FinishEntry `json:"most_title_matches"`
}

// InvalidateRecordsCache should be called after scrape/recalculate to bust the cache.
func InvalidateRecordsCache() {
	recordsCacheMu.Lock()
	recordsCache = nil
	recordsCacheMu.Unlock()
}

// WarmRecordsCache pre-populates the records cache on startup so the first user doesn't wait.
func WarmRecordsCache(db *gorm.DB) {
	go func() {
		log.Println("[stats] Warming records cache...")
		start := time.Now()
		// Trigger an unfiltered request internally by calling the handler logic
		// We'll just hit the endpoint logic directly via a fake gin context — 
		// simpler: just call the raw SQL path and cache it
		handler := GetRecords(db)
		// Create a minimal context to execute the handler
		w := &noopWriter{}
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request, _ = http.NewRequest("GET", "/api/stats/records", nil)
		handler(ctx)
		log.Printf("[stats] Records cache warmed in %v", time.Since(start))
	}()
}

// noopWriter discards the response (used for cache warming)
type noopWriter struct{}
func (n *noopWriter) Header() http.Header        { return http.Header{} }
func (n *noopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *noopWriter) WriteHeader(int)             {}

// GET /api/stats/records?ids=1,2,3 (optional filter)
func GetRecords(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Parse optional wrestler ID filter
		var wrestlerIDs []uint
		if idsParam := c.Query("ids"); idsParam != "" {
			for _, idStr := range strings.Split(idsParam, ",") {
				if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil && id > 0 {
					wrestlerIDs = append(wrestlerIDs, uint(id))
				}
			}
		}

		// For unfiltered requests (or "all IDs" which is effectively unfiltered), use cache
		if len(wrestlerIDs) == 0 || len(wrestlerIDs) > 1000 {
			recordsCacheMu.RLock()
			if recordsCache != nil && time.Since(recordsCacheTime) < recordsCacheTTL {
				cached := *recordsCache
				recordsCacheMu.RUnlock()
				c.JSON(http.StatusOK, cached)
				return
			}
			recordsCacheMu.RUnlock()
		}

		resp := StatsResponse{}

		// Build filter clause with parameterized query
		filterClause := ""
		var filterArgs []interface{}
		if len(wrestlerIDs) > 0 && len(wrestlerIDs) <= 1000 {
			placeholders := make([]string, len(wrestlerIDs))
			for i, id := range wrestlerIDs {
				placeholders[i] = "?"
				filterArgs = append(filterArgs, id)
			}
			filterClause = "AND mp.wrestler_id IN (" + strings.Join(placeholders, ",") + ")"
		}

		// Longest winning streaks
		type streakRow struct {
			WrestlerID uint
			Name       string
			Promotion  string
			MaxStreak  int
		}
		var winStreaks []streakRow
		db.Raw(fmt.Sprintf(`
			WITH wrestler_results AS (
				SELECT 
					mp.wrestler_id,
					m.date,
					m.id as match_id,
					mp.is_winner
				FROM match_participants mp
				JOIN matches m ON m.id = mp.match_id
				WHERE 1=1 %s
				ORDER BY mp.wrestler_id, m.date, m.id
			),
			streak_groups AS (
				SELECT 
					wrestler_id,
					is_winner,
					ROW_NUMBER() OVER (PARTITION BY wrestler_id ORDER BY date, match_id) -
					ROW_NUMBER() OVER (PARTITION BY wrestler_id, is_winner ORDER BY date, match_id) as grp
				FROM wrestler_results
			),
			max_streaks AS (
				SELECT wrestler_id, MAX(cnt) as max_streak
				FROM (
					SELECT wrestler_id, is_winner, grp, COUNT(*) as cnt
					FROM streak_groups
					WHERE is_winner = 1
					GROUP BY wrestler_id, is_winner, grp
				)
				GROUP BY wrestler_id
			)
			SELECT ms.wrestler_id, w.name, w.promotion, ms.max_streak
			FROM max_streaks ms
			JOIN wrestlers w ON w.id = ms.wrestler_id
			WHERE ms.max_streak >= 5
			ORDER BY ms.max_streak DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&winStreaks)

		for _, s := range winStreaks {
			resp.LongestWinStreak = append(resp.LongestWinStreak, StreakEntry{
				WrestlerID: s.WrestlerID, Name: s.Name, Promotion: s.Promotion, Streak: s.MaxStreak,
			})
		}

		// Longest losing streaks
		var loseStreaks []streakRow
		db.Raw(fmt.Sprintf(`
			WITH wrestler_results AS (
				SELECT 
					mp.wrestler_id,
					m.date,
					m.id as match_id,
					mp.is_winner
				FROM match_participants mp
				JOIN matches m ON m.id = mp.match_id
				WHERE 1=1 %s
				ORDER BY mp.wrestler_id, m.date, m.id
			),
			streak_groups AS (
				SELECT 
					wrestler_id,
					is_winner,
					ROW_NUMBER() OVER (PARTITION BY wrestler_id ORDER BY date, match_id) -
					ROW_NUMBER() OVER (PARTITION BY wrestler_id, is_winner ORDER BY date, match_id) as grp
				FROM wrestler_results
			),
			max_streaks AS (
				SELECT wrestler_id, MAX(cnt) as max_streak
				FROM (
					SELECT wrestler_id, is_winner, grp, COUNT(*) as cnt
					FROM streak_groups
					WHERE is_winner = 0
					GROUP BY wrestler_id, is_winner, grp
				)
				GROUP BY wrestler_id
			)
			SELECT ms.wrestler_id, w.name, w.promotion, ms.max_streak
			FROM max_streaks ms
			JOIN wrestlers w ON w.id = ms.wrestler_id
			WHERE ms.max_streak >= 5
			ORDER BY ms.max_streak DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&loseStreaks)

		for _, s := range loseStreaks {
			resp.LongestLoseStreak = append(resp.LongestLoseStreak, StreakEntry{
				WrestlerID: s.WrestlerID, Name: s.Name, Promotion: s.Promotion, Streak: s.MaxStreak,
			})
		}

		// Most KO/TKO wins
		var koWins []FinishEntry
		db.Raw(fmt.Sprintf(`
			SELECT mp.wrestler_id, w.name, w.promotion, COUNT(*) as count
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE mp.is_winner = 1 AND (LOWER(m.finish_type) = 'ko' OR LOWER(m.finish_type) = 'tko') %s
			GROUP BY mp.wrestler_id
			ORDER BY count DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&koWins)
		resp.MostKOWins = koWins

		// Most losses by count out
		var countOuts []FinishEntry
		db.Raw(fmt.Sprintf(`
			SELECT mp.wrestler_id, w.name, w.promotion, COUNT(*) as count
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE mp.is_winner = 0 AND LOWER(m.finish_type) LIKE '%%count out%%' %s
			GROUP BY mp.wrestler_id
			ORDER BY count DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&countOuts)
		resp.MostCountOuts = countOuts

		// Most DQ wins
		var dqWins []FinishEntry
		db.Raw(fmt.Sprintf(`
			SELECT mp.wrestler_id, w.name, w.promotion, COUNT(*) as count
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE mp.is_winner = 1 AND (LOWER(m.finish_type) LIKE '%%disqualification%%' OR LOWER(m.finish_type) LIKE '%%dq%%') %s
			GROUP BY mp.wrestler_id
			ORDER BY count DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&dqWins)
		resp.MostDQWins = dqWins

		// Most DQ losses
		var dqLosses []FinishEntry
		db.Raw(fmt.Sprintf(`
			SELECT mp.wrestler_id, w.name, w.promotion, COUNT(*) as count
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE mp.is_winner = 0 AND (LOWER(m.finish_type) LIKE '%%disqualification%%' OR LOWER(m.finish_type) LIKE '%%dq%%') %s
			GROUP BY mp.wrestler_id
			ORDER BY count DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&dqLosses)
		resp.MostDQLosses = dqLosses

		// Most title matches
		var titleMatches []FinishEntry
		db.Raw(fmt.Sprintf(`
			SELECT mp.wrestler_id, w.name, w.promotion, COUNT(DISTINCT mp.match_id) as count
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE m.is_title_match = 1 %s
			GROUP BY mp.wrestler_id
			ORDER BY count DESC
			LIMIT 10
		`, filterClause), filterArgs...).Scan(&titleMatches)
		resp.MostTitleMatches = titleMatches

		// Cache unfiltered results
		if len(wrestlerIDs) == 0 || len(wrestlerIDs) > 1000 {
			recordsCacheMu.Lock()
			recordsCache = &resp
			recordsCacheTime = time.Now()
			recordsCacheMu.Unlock()
		}

		c.JSON(http.StatusOK, resp)
	}
}
