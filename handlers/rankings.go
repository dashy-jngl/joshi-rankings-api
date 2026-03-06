package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
	"joshi-rankings-api/services"
)

// GET /api/tiers — single source of truth for tier definitions
func GetTiers() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, services.Tiers)
	}
}

// GET /api/rankings?promotion=stardom&limit=10
func GetRankings(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var wrestlers []models.Wrestler
		query := db.Order("elo DESC").Preload("Aliases")

		if promotion := c.Query("promotion"); promotion != "" {
			query = query.Where("promotion = ?", promotion)
		}
		if limitStr := c.Query("limit"); limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil {
				query = query.Limit(limit)
			}
		}

		if err := query.Find(&wrestlers).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve rankings"})
			return
		}
		rankings := services.BuildRankings(db,wrestlers)
		c.JSON(http.StatusOK, rankings)
	}
}