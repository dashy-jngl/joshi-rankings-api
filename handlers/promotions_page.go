package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GET /api/promotions/stats — promotion stats list
func GetPromotionStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		sort := c.DefaultQuery("sort", "wrestlers")
		limit := 50
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		country := c.Query("country")
		region := c.Query("region")

		orderClause := "wrestler_count DESC"
		switch sort {
		case "elo":
			orderClause = "avg_elo DESC"
		case "titles":
			orderClause = "title_count DESC"
		case "wrestlers":
			orderClause = "wrestler_count DESC"
		}

		type PromoStat struct {
			ID            uint    `json:"id"`
			CagematchID   int     `json:"cagematch_id"`
			Name          string  `json:"name"`
			Abbreviation  string  `json:"abbreviation"`
			Country       string  `json:"country"`
			Region        string  `json:"region"`
			Status        string  `json:"status"`
			WrestlerCount int     `json:"wrestler_count"`
			AvgElo        float64 `json:"avg_elo"`
			TitleCount    int     `json:"title_count"`
		}

		where := "1=1"
		args := []interface{}{}
		if country != "" {
			where += " AND p.country = ?"
			args = append(args, country)
		}
		if region != "" {
			where += " AND p.region = ?"
			args = append(args, region)
		}

		// Use pre-aggregated joins instead of correlated subqueries
		query := fmt.Sprintf(`
			SELECT p.id, p.cagematch_id, p.name, p.abbreviation, p.country, p.region, p.status,
				COALESCE(ph_agg.wrestler_count, 0) as wrestler_count,
				COALESCE(w_agg.avg_elo, 0) as avg_elo,
				COALESCE(t_agg.title_count, 0) as title_count
			FROM promotions p
			LEFT JOIN (
				SELECT promotion, COUNT(DISTINCT wrestler_id) as wrestler_count
				FROM promotion_histories GROUP BY promotion
			) ph_agg ON ph_agg.promotion = p.name
			LEFT JOIN (
				SELECT promotion, AVG(elo) as avg_elo
				FROM wrestlers GROUP BY promotion
			) w_agg ON w_agg.promotion = p.name
			LEFT JOIN (
				SELECT promotion, COUNT(*) as title_count
				FROM titles GROUP BY promotion
			) t_agg ON t_agg.promotion = p.name
			WHERE %s
			ORDER BY %s
			LIMIT ?
		`, where, orderClause)
		args = append(args, limit)

		var stats []PromoStat
		db.Raw(query, args...).Scan(&stats)

		c.JSON(http.StatusOK, stats)
	}
}

// GET /api/promotions/:id — promotion profile
func GetPromotion(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		promoID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid promotion ID"})
			return
		}

		// Promotion metadata
		type PromoMeta struct {
			ID           uint   `json:"id"`
			CagematchID  int    `json:"cagematch_id"`
			Name         string `json:"name"`
			Abbreviation string `json:"abbreviation"`
			Location     string `json:"location"`
			Country      string `json:"country"`
			Region       string `json:"region"`
			Status       string `json:"status"`
			ActiveFrom   string `json:"active_from"`
			ActiveTo     string `json:"active_to"`
		}
		var meta PromoMeta
		db.Raw(`SELECT * FROM promotions WHERE cagematch_id = ?`, promoID).Scan(&meta)

		if meta.Name == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Promotion not found"})
			return
		}

		// Total wrestlers from promotion_histories
		var totalWrestlers int
		db.Raw(`SELECT COUNT(DISTINCT wrestler_id) FROM promotion_histories WHERE promotion = ?`, meta.Name).Scan(&totalWrestlers)

		// Current roster (wrestlers whose promotion field matches)
		type RosterEntry struct {
			ID        uint    `json:"id"`
			Name      string  `json:"name"`
			Elo       float64 `json:"elo"`
			Wins      int     `json:"wins"`
			Losses    int     `json:"losses"`
			Draws     int     `json:"draws"`
			Promotion string  `json:"promotion"`
		}
		var roster []RosterEntry
		db.Raw(`
			SELECT id, name, elo, wins, losses, draws, promotion
			FROM wrestlers WHERE promotion = ?
			ORDER BY elo DESC
		`, meta.Name).Scan(&roster)

		// Avg ELO of current roster
		var avgElo float64
		if len(roster) > 0 {
			db.Raw(`SELECT AVG(elo) FROM wrestlers WHERE promotion = ?`, meta.Name).Scan(&avgElo)
		}

		// Top 10 by ELO
		top10 := roster
		if len(top10) > 10 {
			top10 = top10[:10]
		}

		// Titles associated
		type TitleEntry struct {
			CagematchID int    `json:"cagematch_id"`
			Name        string `json:"name"`
			Status      string `json:"status"`
		}
		var titles []TitleEntry
		db.Raw(`SELECT cagematch_id, name, status FROM titles WHERE promotion = ? ORDER BY name`, meta.Name).Scan(&titles)

		// Year-by-year wrestler count
		type YearCount struct {
			Year      int `json:"year"`
			Wrestlers int `json:"wrestlers"`
			Matches   int `json:"matches"`
		}
		var yearCounts []YearCount
		db.Raw(`
			SELECT year, COUNT(DISTINCT wrestler_id) as wrestlers, SUM(matches) as matches
			FROM promotion_histories WHERE promotion = ?
			GROUP BY year ORDER BY year
		`, meta.Name).Scan(&yearCounts)

		c.JSON(http.StatusOK, gin.H{
			"promotion":       meta,
			"total_wrestlers": totalWrestlers,
			"current_roster":  roster,
			"avg_elo":         avgElo,
			"top_10":          top10,
			"titles":          titles,
			"year_by_year":    yearCounts,
		})
	}
}
