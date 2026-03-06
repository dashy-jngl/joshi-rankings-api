package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
	"joshi-rankings-api/scraper"
)

// GetScrapeDelay returns the current scrape delay in seconds.
func GetScrapeDelay(db *gorm.DB, cm *scraper.CagematchScraper) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"seconds": int(cm.GetDelay().Seconds()),
		})
	}
}

// SetScrapeDelay updates the scrape delay, persists to DB, and updates the live scraper.
func SetScrapeDelay(db *gorm.DB, cm *scraper.CagematchScraper) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Seconds int `json:"seconds" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "seconds required"})
			return
		}
		if req.Seconds < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "seconds must be non-negative"})
			return
		}

		// Persist to DB
		setting := models.Setting{
			Key:   "scrape_delay_seconds",
			Value: fmt.Sprintf("%d", req.Seconds),
		}
		db.Where("key = ?", "scrape_delay_seconds").Assign(setting).FirstOrCreate(&setting)

		// Update live scraper
		cm.SetDelay(time.Duration(req.Seconds) * time.Second)

		c.JSON(http.StatusOK, gin.H{
			"message": fmt.Sprintf("Scrape delay updated to %ds", req.Seconds),
			"seconds": req.Seconds,
		})
	}
}
