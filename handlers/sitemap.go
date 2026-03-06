package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

func Sitemap(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		now := time.Now().Format("2006-01-02")

		staticPages := []struct {
			Loc        string
			ChangeFreq string
			Priority   string
		}{
			{"/", "daily", "1.0"},
			{"/rankings", "daily", "0.9"},
			{"/compare", "weekly", "0.7"},
			{"/network", "weekly", "0.7"},
			{"/timeline", "weekly", "0.7"},
			{"/stats", "daily", "0.8"},
			{"/predictor", "weekly", "0.6"},
			{"/titles", "daily", "0.8"},
			{"/promotions", "weekly", "0.8"},
			{"/contact", "monthly", "0.3"},
		}

		xml := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`

		for _, p := range staticPages {
			xml += fmt.Sprintf(`
  <url>
    <loc>https://joshitori.com%s</loc>
    <lastmod>%s</lastmod>
    <changefreq>%s</changefreq>
    <priority>%s</priority>
  </url>`, p.Loc, now, p.ChangeFreq, p.Priority)
		}

		// Wrestlers (exclude ghost wrestler ID 0)
		var wrestlers []models.Wrestler
		db.Select("id").Where("id > 0").Find(&wrestlers)
		for _, w := range wrestlers {
			xml += fmt.Sprintf(`
  <url>
    <loc>https://joshitori.com/wrestler/%d</loc>
    <lastmod>%s</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.6</priority>
  </url>`, w.ID, now)
		}

		// Titles
		var titles []models.Title
		db.Select("id").Find(&titles)
		for _, t := range titles {
			xml += fmt.Sprintf(`
  <url>
    <loc>https://joshitori.com/title/%d</loc>
    <lastmod>%s</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.5</priority>
  </url>`, t.ID, now)
		}

		// Promotions
		var promotions []models.Promotion
		db.Select("id").Find(&promotions)
		for _, p := range promotions {
			xml += fmt.Sprintf(`
  <url>
    <loc>https://joshitori.com/promotion/%d</loc>
    <lastmod>%s</lastmod>
    <changefreq>weekly</changefreq>
    <priority>0.5</priority>
  </url>`, p.ID, now)
		}

		xml += `
</urlset>`

		c.Data(http.StatusOK, "application/xml; charset=utf-8", []byte(xml))
	}
}
