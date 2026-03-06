package handlers

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

// Cached templates — loaded once at startup
var (
	wrestlerTemplate  string
	titleTemplate     string
	promotionTemplate string
	eventTemplate     string
)

func InitSEOTemplates() {
	wrestlerTemplate = loadTemplate("./static/wrestler.html")
	titleTemplate = loadTemplate("./static/title.html")
	promotionTemplate = loadTemplate("./static/promotion.html")
	eventTemplate = loadTemplate("./static/event.html")
}

func loadTemplate(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func injectMeta(tmpl, name, desc, path string) string {
	safeName := html.EscapeString(name)
	safeDesc := html.EscapeString(desc)
	url := "https://joshitori.com" + path

	s := tmpl
	// Title tag
	s = replaceFirst(s, "<title>", "</title>", fmt.Sprintf("<title>%s</title>", safeName))
	// Meta description
	s = replaceAttr(s, `name="description"`, safeDesc)
	// OG tags
	s = replaceAttr(s, `property="og:title"`, safeName)
	s = replaceAttr(s, `property="og:description"`, safeDesc)
	s = replaceAttr(s, `property="og:url"`, url)
	// Twitter tags
	s = replaceAttr(s, `name="twitter:title"`, safeName)
	s = replaceAttr(s, `name="twitter:description"`, safeDesc)
	// Canonical
	s = strings.Replace(s, `rel="canonical" href="https://joshitori.com/wrestler"`, `rel="canonical" href="`+url+`"`, 1)
	s = strings.Replace(s, `rel="canonical" href="https://joshitori.com/title"`, `rel="canonical" href="`+url+`"`, 1)
	s = strings.Replace(s, `rel="canonical" href="https://joshitori.com/promotion"`, `rel="canonical" href="`+url+`"`, 1)
	s = strings.Replace(s, `rel="canonical" href="https://joshitori.com/event"`, `rel="canonical" href="`+url+`"`, 1)
	return s
}

// replaceFirst replaces everything between open and close tags (inclusive)
func replaceFirst(s, open, close, replacement string) string {
	i := strings.Index(s, open)
	if i == -1 {
		return s
	}
	j := strings.Index(s[i:], close)
	if j == -1 {
		return s
	}
	return s[:i] + replacement + s[i+j+len(close):]
}

// replaceAttr replaces content="..." for a meta tag identified by attr
func replaceAttr(s, attr, newContent string) string {
	idx := strings.Index(s, attr)
	if idx == -1 {
		return s
	}
	// Find content="..." after this attr
	sub := s[idx:]
	ci := strings.Index(sub, `content="`)
	if ci == -1 {
		return s
	}
	start := idx + ci + len(`content="`)
	end := strings.Index(s[start:], `"`)
	if end == -1 {
		return s
	}
	return s[:start] + newContent + s[start+end:]
}

// ServeWrestlerPage serves wrestler.html with injected SEO meta tags
func ServeWrestlerPage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || wrestlerTemplate == "" {
			c.File("./static/wrestler.html")
			return
		}

		var w models.Wrestler
		if err := db.Select("id, name, promotion, elo").First(&w, id).Error; err != nil {
			c.File("./static/wrestler.html")
			return
		}

		title := fmt.Sprintf("%s | Joshitori", w.Name)
		desc := fmt.Sprintf("%s — ELO %.0f", w.Name, w.ELO)
		if w.Promotion != "" {
			desc += fmt.Sprintf(" | %s", w.Promotion)
		}
		desc += " — Joshi wrestling stats, match history & rankings on Joshitori."

		result := injectMeta(wrestlerTemplate, title, desc, fmt.Sprintf("/wrestler/%d", id))
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(result))
	}
}

// ServeTitlePage serves title.html with injected SEO meta tags
func ServeTitlePage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || titleTemplate == "" {
			c.File("./static/title.html")
			return
		}

		var t models.Title
		if err := db.Select("id, name, promotion").First(&t, id).Error; err != nil {
			c.File("./static/title.html")
			return
		}

		title := fmt.Sprintf("%s | Joshitori", t.Name)
		desc := fmt.Sprintf("%s championship title history, holders, and ELO prestige rating", t.Name)
		if t.Promotion != "" {
			desc += fmt.Sprintf(" — %s", t.Promotion)
		}
		desc += " | Joshitori."

		result := injectMeta(titleTemplate, title, desc, fmt.Sprintf("/title/%d", id))
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(result))
	}
}

// ServePromotionPage serves promotion.html with injected SEO meta tags
func ServePromotionPage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || promotionTemplate == "" {
			c.File("./static/promotion.html")
			return
		}

		var p models.Promotion
		if err := db.Select("id, name, country").First(&p, id).Error; err != nil {
			c.File("./static/promotion.html")
			return
		}

		title := fmt.Sprintf("%s | Joshitori", p.Name)
		desc := fmt.Sprintf("%s — roster, titles, and stats for this women's pro wrestling promotion", p.Name)
		if p.Country != "" {
			desc += fmt.Sprintf(" based in %s", p.Country)
		}
		desc += " | Joshitori."

		result := injectMeta(promotionTemplate, title, desc, fmt.Sprintf("/promotion/%d", id))
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(result))
	}
}

// ServeEventPage serves event.html with injected SEO meta tags
func ServeEventPage(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || eventTemplate == "" {
			c.File("./static/event.html")
			return
		}

		// Events might be matches or a separate model — fallback to generic
		title := fmt.Sprintf("Event #%d | Joshitori", id)
		desc := "View event results, match card, and ELO changes on Joshitori."

		result := injectMeta(eventTemplate, title, desc, fmt.Sprintf("/event/%d", id))
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(result))
	}
}
