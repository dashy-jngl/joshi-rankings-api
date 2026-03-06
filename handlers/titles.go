package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GET /api/titles/stats — title stats list
func GetTitleStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sort := c.DefaultQuery("sort", "reigns")
		limit := 50
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		promotion := c.Query("promotion")

		orderClause := "total_reigns DESC"
		switch sort {
		case "duration":
			orderClause = "avg_duration DESC"
		case "elo":
			orderClause = "avg_holder_elo DESC"
		case "changes":
			orderClause = "total_reigns DESC"
		case "reigns":
			orderClause = "total_reigns DESC"
		}

		type TitleStat struct {
			CagematchTitleID int     `json:"cagematch_title_id"`
			TitleName        string  `json:"title_name"`
			Promotion        string  `json:"promotion"`
			Status           string  `json:"status"`
			TotalReigns      int     `json:"total_reigns"`
			TrackedReigns    int     `json:"tracked_reigns"`
			AvgDuration      float64 `json:"avg_duration"`
			AvgHolderElo     float64 `json:"avg_holder_elo"`
			LongestReignDays int     `json:"longest_reign_days"`
			LongestHolder    string  `json:"longest_holder"`
			CurrentHolder    string  `json:"current_holder"`
		}

		// Group by won_date to count tag reigns as one reign
		query := `
			SELECT 
				grouped.cagematch_title_id,
				grouped.title_name,
				COALESCE(t.promotion, '') as promotion,
				COALESCE(t.status, '') as status,
				COUNT(*) as total_reigns,
				SUM(CASE WHEN grouped.has_tracked > 0 THEN 1 ELSE 0 END) as tracked_reigns,
				AVG(CASE WHEN grouped.duration_days > 0 THEN grouped.duration_days END) as avg_duration,
				AVG(grouped.avg_elo) as avg_holder_elo,
				MAX(grouped.duration_days) as longest_reign_days
			FROM (
				SELECT cagematch_title_id, title_name, won_date,
					MAX(duration_days) as duration_days,
					AVG(CASE WHEN wrestler_id > 0 THEN (SELECT w.elo FROM wrestlers w WHERE w.id = tr.wrestler_id) END) as avg_elo,
					SUM(CASE WHEN untracked = 0 OR untracked IS NULL THEN 1 ELSE 0 END) as has_tracked
				FROM title_reigns tr
				GROUP BY cagematch_title_id, won_date
			) grouped
			LEFT JOIN titles t ON t.cagematch_id = grouped.cagematch_title_id
		`
		args := []interface{}{}
		if promotion != "" {
			query += " WHERE t.promotion = ?"
			args = append(args, promotion)
		}
		query += fmt.Sprintf(` GROUP BY grouped.cagematch_title_id ORDER BY %s LIMIT ?`, orderClause)
		args = append(args, limit)

		var stats []TitleStat
		db.Raw(query, args...).Scan(&stats)

		// Fill longest holder + current holder
		for i, s := range stats {
			var holders []struct{ Name string }
			db.Raw(`
				SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as name
				FROM title_reigns tr
				LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
				WHERE tr.cagematch_title_id = ? AND tr.duration_days = ?
				AND tr.won_date = (
					SELECT tr2.won_date FROM title_reigns tr2 
					WHERE tr2.cagematch_title_id = ? AND tr2.duration_days = ?
					LIMIT 1
				)
			`, s.CagematchTitleID, s.LongestReignDays, s.CagematchTitleID, s.LongestReignDays).Scan(&holders)
			names := make([]string, len(holders))
			for j, h := range holders {
				names[j] = h.Name
			}
			stats[i].LongestHolder = strings.Join(dedupStrings(names), " & ")

			var curHolders []struct{ Name string }
			db.Raw(`
				SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as name
				FROM title_reigns tr
				LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
				WHERE tr.cagematch_title_id = ? AND tr.lost_date IS NULL
				ORDER BY tr.won_date DESC
			`, s.CagematchTitleID).Scan(&curHolders)
			curNames := make([]string, len(curHolders))
			for j, h := range curHolders {
				curNames[j] = h.Name
			}
			stats[i].CurrentHolder = strings.Join(dedupStrings(curNames), " & ")
		}

		c.JSON(http.StatusOK, stats)
	}
}

// GET /api/titles/:id — title profile
// Returns raw reigns — frontend groups tag partners by won_date
func GetTitle(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		titleID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid title ID"})
			return
		}

		// Title metadata
		type TitleMeta struct {
			CagematchID int    `json:"cagematch_id"`
			Name        string `json:"name"`
			Promotion   string `json:"promotion"`
			PromotionID int    `json:"promotion_id"`
			Status      string `json:"status"`
		}
		var meta TitleMeta
		db.Raw(`SELECT cagematch_id, name, promotion, promotion_id, status FROM titles WHERE cagematch_id = ?`, titleID).Scan(&meta)

		if meta.Name == "" {
			db.Raw(`SELECT cagematch_title_id as cagematch_id, title_name as name FROM title_reigns WHERE cagematch_title_id = ? LIMIT 1`, titleID).Scan(&meta)
		}

		if meta.Name == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Title not found"})
			return
		}

		// Stats — group by won_date for accurate count
		type TitleStats struct {
			TotalReigns   int     `json:"total_reigns"`
			TrackedReigns int     `json:"tracked_reigns"`
			AvgHolderElo  float64 `json:"avg_holder_elo"`
			AvgDuration   float64 `json:"avg_duration"`
		}
		var stats TitleStats
		db.Raw(`
			SELECT COUNT(*) as total_reigns,
				SUM(CASE WHEN has_tracked > 0 THEN 1 ELSE 0 END) as tracked_reigns,
				AVG(CASE WHEN duration_days > 0 THEN duration_days END) as avg_duration,
				AVG(avg_elo) as avg_holder_elo
			FROM (
				SELECT won_date,
					MAX(duration_days) as duration_days,
					AVG(CASE WHEN wrestler_id > 0 THEN (SELECT w.elo FROM wrestlers w WHERE w.id = tr.wrestler_id) END) as avg_elo,
					SUM(CASE WHEN untracked = 0 OR untracked IS NULL THEN 1 ELSE 0 END) as has_tracked
				FROM title_reigns tr
				WHERE cagematch_title_id = ?
				GROUP BY won_date
			)
		`, titleID).Scan(&stats)

		// Longest reign
		type LongestReign struct {
			WrestlerName string `json:"wrestler_name"`
			WrestlerID   uint   `json:"wrestler_id"`
			Days         int    `json:"days"`
		}
		var longestRaw []struct {
			WrestlerName string
			WrestlerID   uint
			Days         int
		}
		db.Raw(`
			SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as wrestler_name, 
			       tr.wrestler_id, tr.duration_days as days
			FROM title_reigns tr
			LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
			WHERE tr.cagematch_title_id = ? AND tr.won_date = (
				SELECT tr2.won_date FROM title_reigns tr2 
				WHERE tr2.cagematch_title_id = ?
				ORDER BY tr2.duration_days DESC LIMIT 1
			)
			ORDER BY tr.duration_days DESC
		`, titleID, titleID).Scan(&longestRaw)
		longest := LongestReign{}
		if len(longestRaw) > 0 {
			names := make([]string, len(longestRaw))
			for i, r := range longestRaw {
				names[i] = r.WrestlerName
			}
			longest.WrestlerName = strings.Join(dedupStrings(names), " & ")
			longest.WrestlerID = longestRaw[0].WrestlerID
			longest.Days = longestRaw[0].Days
		}

		// Most reigns
		type MostReigns struct {
			WrestlerName string `json:"wrestler_name"`
			WrestlerID   uint   `json:"wrestler_id"`
			Count        int    `json:"count"`
		}
		var most MostReigns
		db.Raw(`
			SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as wrestler_name,
			       tr.wrestler_id, COUNT(DISTINCT tr.won_date) as count
			FROM title_reigns tr
			LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
			WHERE tr.cagematch_title_id = ? AND tr.wrestler_id > 0
			GROUP BY tr.wrestler_id
			ORDER BY count DESC LIMIT 1
		`, titleID).Scan(&most)

		// Current holders — deduplicated
		type CurrentHolder struct {
			WrestlerName string `json:"wrestler_name"`
			WrestlerID   uint   `json:"wrestler_id"`
			WonDate      string `json:"won_date"`
		}
		var currentHolders []CurrentHolder
		db.Raw(`
			SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as wrestler_name,
			       tr.wrestler_id, tr.won_date
			FROM title_reigns tr
			LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
			WHERE tr.cagematch_title_id = ? AND tr.lost_date IS NULL
			ORDER BY tr.won_date DESC
		`, titleID).Scan(&currentHolders)

		// All reigns — raw rows, frontend will group by won_date for tags
		type Reign struct {
			WrestlerName string  `json:"wrestler_name"`
			WrestlerID   uint    `json:"wrestler_id"`
			ReignNumber  int     `json:"reign_number"`
			WonDate      string  `json:"won_date"`
			LostDate     *string `json:"lost_date"`
			DurationDays int     `json:"duration_days"`
			Untracked    bool    `json:"untracked"`
			Elo          float64 `json:"elo"`
		}
		var reigns []Reign
		db.Raw(`
			SELECT COALESCE(w.name, tr.holder_name, 'Unknown') as wrestler_name,
			       tr.wrestler_id, tr.reign_number, tr.won_date, tr.lost_date,
			       tr.duration_days, tr.untracked,
			       COALESCE(w.elo, 0) as elo
			FROM title_reigns tr
			LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
			WHERE tr.cagematch_title_id = ?
			AND UPPER(COALESCE(tr.holder_name, '')) != 'VACANT'
			ORDER BY tr.won_date DESC, tr.wrestler_id
		`, titleID).Scan(&reigns)

		c.JSON(http.StatusOK, gin.H{
			"title":           meta,
			"stats":           stats,
			"longest_reign":   longest,
			"most_reigns":     most,
			"current_holders": currentHolders,
			"reigns":          reigns,
		})
	}
}

// dedupStrings removes duplicate strings while preserving order
func dedupStrings(input []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
