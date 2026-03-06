package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
	"joshi-rankings-api/services"
)

// GET /api/matches?wrestler_id=1
func GetMatches(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var matches []models.Match
		query := db.Preload("Participants")
		if wrestlerID := c.Query("wrestler_id"); wrestlerID != "" {
			query = query.Where("id IN (?)", db.Table("match_participants").Select("match_id").Where("wrestler_id = ?", wrestlerID))
		}

		// Apply limit (default 100 when no wrestler_id filter)
		limit := 100
		if l := c.Query("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
				limit = parsed
			}
		} else if c.Query("wrestler_id") != "" {
			limit = 0 // no limit when filtering by wrestler
		}
		if limit > 0 {
			query = query.Limit(limit)
		}

		if err := query.Order("date DESC").Find(&matches).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve matches"})
			return
		}

		// Collect all wrestler IDs to fetch names
		idSet := map[uint]bool{}
		for _, m := range matches {
			for _, p := range m.Participants {
				idSet[p.WrestlerID] = true
			}
		}
		ids := make([]uint, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		nameMap := map[uint]string{}
		if len(ids) > 0 {
			var wrestlers []models.Wrestler
			db.Select("id, name").Where("id IN ?", ids).Find(&wrestlers)
			for _, w := range wrestlers {
				nameMap[w.ID] = w.Name
			}
		}

		// Build response with wrestler names
		type ParticipantResp struct {
			ID               uint    `json:"id"`
			MatchID          uint    `json:"match_id"`
			WrestlerID       uint    `json:"wrestler_id"`
			WrestlerName     string  `json:"wrestler_name"`
			Team             int     `json:"team"`
			IsWinner         bool    `json:"is_winner"`
			EloChange        float64 `json:"elo_change"`
			GhostName        string  `json:"ghost_name,omitempty"`
			GhostCagematchID int     `json:"ghost_cagematch_id,omitempty"`
		}
		type MatchResp struct {
			models.Match
			Participants []ParticipantResp `json:"participants"`
		}

		resp := make([]MatchResp, len(matches))
		for i, m := range matches {
			parts := make([]ParticipantResp, len(m.Participants))
			for j, p := range m.Participants {
				name := nameMap[p.WrestlerID]
				if p.WrestlerID == 0 && p.GhostName != "" {
					name = p.GhostName
				}
				parts[j] = ParticipantResp{
					ID:               p.ID,
					MatchID:          p.MatchID,
					WrestlerID:       p.WrestlerID,
					WrestlerName:     name,
					Team:             p.Team,
					IsWinner:         p.IsWinner,
					EloChange:        p.EloChange,
					GhostName:        p.GhostName,
					GhostCagematchID: p.GhostCagematchID,
				}
			}
			resp[i] = MatchResp{Match: m}
			resp[i].Participants = parts
		}

		c.JSON(http.StatusOK, resp)
	}
}

// GET /api/matches/count
func GetMatchCount(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var count int64
		db.Model(&models.Match{}).Count(&count)
		c.JSON(http.StatusOK, gin.H{"count": count})
	}
}

func GetSkippedCount(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var count int64
		db.Model(&models.SkippedWrestler{}).Count(&count)
		c.JSON(http.StatusOK, gin.H{"count": count})
	}
}

// POST /api/matches
func CreateMatch(db *gorm.DB) gin.HandlerFunc {
	calc := services.NewELOCalculator()

	return func(c *gin.Context) {
		var match models.Match
		if err := c.ShouldBindJSON(&match); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}

		// Validate participants
		if len(match.Participants) < 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "At least 2 participants are required"})
			return
		}

		//split winners and losers
		var winners, losers []models.Wrestler
		for _, p := range match.Participants {
			var wrestler models.Wrestler
			if err := db.First(&wrestler, p.WrestlerID).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "Wrestler not found", "wrestler_id": p.WrestlerID})
				return
			}
			if p.IsWinner {
				winners = append(winners, wrestler)
			} else {
				losers = append(losers, wrestler)
			}
		}

		//validate we have at least 1 winner and 1 loser
		if !match.IsDraw && (len(winners) == 0 || len(losers) == 0) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "At least one winner and one loser is required"})
			return
		}

		var eloResults []services.ELOResult
		// calculate new ELOs
		if match.IsDraw {
			var team1,team2 []models.Wrestler
			for _, p := range match.Participants {
				var wrestler models.Wrestler
				db.First(&wrestler, p.WrestlerID)
				if p.Team == 1 {
					team1 = append(team1, wrestler)
				} else {
					team2 = append(team2, wrestler)
				}
			}
			eloResults = calc.CalculateMulti(team1, team2, match.IsTitleMatch, match.IsDraw)
		} else {
			eloResults = calc.CalculateMulti(winners, losers, match.IsTitleMatch, match.IsDraw)
		}
		//save everything in a transaction
		err := db.Transaction(func(tx *gorm.DB) error {
			// Create the match
			if err := tx.Create(&match).Error; err != nil {
				return err
			}

			for _, r := range eloResults {
				// update wrestler ELO and wins/losses
				updates := map[string]interface{}{
					"elo": r.NewELO,
				}
				if match.IsDraw {
					tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).Update("draws", gorm.Expr("draws + ?", 1))
				} else if r.IsWinner {
					tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).Update("wins", gorm.Expr("wins + ?", 1))
				} else {
					tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).Update("losses", gorm.Expr("losses + ?", 1))
				}
				tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).Updates(updates)

				// save ELO history
				tx.Create(&models.ELOHistory{
					WrestlerID: r.WrestlerID,
					ELO: r.NewELO,
					MatchID: match.ID,
				})
			}

			return nil
		})
		
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create match"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"match": match,
			"elo_results": eloResults,
		})
	}
}
				
func DeleteMatch(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid match ID"})
			return
		}
		
		if err := db.Delete(&models.Match{}, id).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete match"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Match deleted"})
	}
}