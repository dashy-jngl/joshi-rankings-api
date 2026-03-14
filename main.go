package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"gorm.io/gorm"

	"joshi-rankings-api/database"
	"joshi-rankings-api/handlers"
	"joshi-rankings-api/middleware"
	"joshi-rankings-api/models"
	"joshi-rankings-api/scraper"
	"joshi-rankings-api/tasklog"
)

// Scraper state — track status and control the cron
var (
	lastScrapeTime    time.Time
	lastScrapeMatches int
	cronRunning       bool
	cronStop          chan struct{}
	cronMu            sync.Mutex

	// Scheduled job tracking
	lastMatchScrapeTime    time.Time
	lastMatchScrapeMatches int
	lastProfileRefreshTime time.Time
	lastProfileRefreshCount int
	matchScrapeRunning     bool
	profileRefreshRunning  bool
	nextMatchScrape        time.Time
	nextProfileRefresh     time.Time
)

// Test scrape state
var (
	testScrapeResults   []gin.H
	testValidateResults []gin.H
	testMu              sync.Mutex
)

// PFP update state
var (
	pfpMu        sync.Mutex
	pfpRunning   bool
	pfpTotal     int
	pfpProcessed int
	pfpSuccess   int
	pfpClosed    int
	pfpFailed    int
	pfpLog       []gin.H
)

func main() {
	godotenv.Load()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "joshi.db"
	}

	db, err := database.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Scraper setup — NOT started automatically
	cm := scraper.NewCagematchScraper(db)
	proc := scraper.NewProcessor(db, cm)

	// Load persisted scrape delay from DB
	var delaySetting models.Setting
	if err := db.Where("key = ?", "scrape_delay_seconds").First(&delaySetting).Error; err == nil {
		if secs, err := strconv.Atoi(delaySetting.Value); err == nil {
			cm.SetDelay(time.Duration(secs) * time.Second)
			log.Printf("🕷️ Loaded scrape delay from DB: %ds", secs)
		}
	}

	// Load SEO templates for server-side meta injection
	handlers.InitSEOTemplates()

	// Router
	r := gin.Default()
	r.SetTrustedProxies(nil)
	r.Use(middleware.CORS())
	r.Use(middleware.CSRFProtection())

	// Serve dashboard at root
	r.Static("/static", "./static")
	r.GET("/", func(c *gin.Context) {
		c.File("./static/index.html")
	})
	r.GET("/rankings", func(c *gin.Context) {
		c.File("./static/rankings.html")
	})
	r.GET("/admin", middleware.SessionAuth(), middleware.RequireRole("admin"), func(c *gin.Context) {
		c.File("./static/admin.html")
	})
	r.GET("/wrestler/:id", handlers.ServeWrestlerPage(db))
	r.GET("/compare", func(c *gin.Context) {
		c.File("./static/compare.html")
	})
	r.GET("/network", func(c *gin.Context) {
		c.File("./static/network.html")
	})
	r.GET("/stats", func(c *gin.Context) {
		c.File("./static/stats.html")
	})
	r.GET("/timeline", func(c *gin.Context) {
		c.File("./static/timeline.html")
	})
	r.GET("/predictor", func(c *gin.Context) {
		c.File("./static/predictor.html")
	})
	r.GET("/titles", func(c *gin.Context) {
		c.File("./static/titles.html")
	})
	r.GET("/title/:id", handlers.ServeTitlePage(db))
	r.GET("/promotions", func(c *gin.Context) {
		c.File("./static/promotions.html")
	})
	r.GET("/promotion/:id", handlers.ServePromotionPage(db))
	r.GET("/event/:id", handlers.ServeEventPage(db))
	r.GET("/contact", func(c *gin.Context) {
		c.File("./static/contact.html")
	})

	// SEO
	r.GET("/robots.txt", func(c *gin.Context) {
		c.File("./static/robots.txt")
	})
	r.GET("/sitemap.xml", handlers.Sitemap(db))

	api := r.Group("/api")
	{
		// Public
		api.GET("/tasklog", handleTaskLog())
		api.GET("/wrestlers", handlers.GetWrestlers(db))
		api.GET("/wrestler-names", handlers.GetWrestlerNames(db))
		api.GET("/wrestler-slim", handlers.GetWrestlerSlim(db))
		api.GET("/wrestlers/:id", handlers.GetWrestler(db))
		api.GET("/wrestlers/:id/elo-history", handlers.GetWrestlerELOHistory(db))
		api.GET("/wrestlers/:id/stats", handlers.GetWrestlerStats(db))
		api.GET("/wrestlers/:id/promotions", handlers.GetWrestlerPromotions(db))
		api.GET("/wrestlers/:id/titles", handlers.GetWrestlerTitles(db))
		api.GET("/titles", handlers.GetTitles(db))
		api.GET("/titles/stats", handlers.GetTitleStats(db))
		api.GET("/titles/:id", handlers.GetTitle(db))
		api.GET("/titles/:id/elo-history", handlers.GetTitleELOHistory(db))
		api.GET("/promotions", handlers.GetPromotions(db))
		api.GET("/promotions/stats", handlers.GetPromotionStats(db))
		api.GET("/promotions/roster", handlers.GetPromotionRoster(db))
		api.GET("/promotions/list", handlers.GetPromotionList(db))
		api.GET("/promotions/:id", handlers.GetPromotion(db))
		api.GET("/events/:id", handlers.GetEvent(db))
		api.GET("/elo-history", handlers.GetMultiELOHistory(db))
		api.GET("/matches", handlers.GetMatches(db))
		api.GET("/matches/count", handlers.GetMatchCount(db))
		api.GET("/skipped/count", handlers.GetSkippedCount(db))
		api.GET("/rankings", handlers.GetRankings(db))
		api.GET("/featured", handlers.GetFeatured(db))
		api.GET("/tiers", handlers.GetTiers())

		// Timeline endpoints
		api.GET("/timeline/elo-leaders", handlers.GetELOLeaders(db))
		api.GET("/timeline/era-stats", handlers.GetEraStats(db))
		api.GET("/timeline/snapshot", handlers.GetELOSnapshot(db))

		// Network endpoints
		api.GET("/stats/records", handlers.GetRecords(db))
		api.GET("/network/wrestler/:id", handlers.GetWrestlerNetwork(db))
		api.GET("/network/top", handlers.GetTopNetwork(db))
		api.GET("/network/rivalries", handlers.GetRivalries(db))
		api.GET("/network/head-to-head", handlers.GetHeadToHead(db))

		// Predictor endpoints (public)
		api.POST("/predict/match", handlers.PredictMatch(db))
		api.POST("/predict/multi", handlers.PredictMulti(db))
		api.POST("/predict/tournament", handlers.PredictTournament(db))

		// Auth endpoints
		api.POST("/auth/login", middleware.RateLimit(0.1, 5), handlers.Login(db))
		api.POST("/auth/setup", middleware.RateLimit(0.05, 3), handlers.Setup(db))
		api.POST("/auth/logout", handlers.Logout())
		api.GET("/auth/me", handlers.Me())

		// Moderator group — wrestler moderation tools
		moderator := api.Group("/mod")
		moderator.Use(middleware.SessionAuth(), middleware.RequireRole("admin", "moderator"))
		{
			moderator.PUT("/wrestlers/:id/image", handlers.SetWrestlerImage(db))
			moderator.POST("/wrestlers/:id/ghost", handlers.GhostWrestler(db))
		}

		// Admin group — session auth + admin role required
		admin := api.Group("/admin")
		admin.Use(middleware.SessionAuth(), middleware.RequireRole("admin"))
		{
			admin.GET("/users", handlers.ListUsers(db))
			admin.POST("/users", handlers.CreateUser(db))
			admin.PUT("/users/:id", handlers.UpdateUser(db))
			admin.DELETE("/users/:id", handlers.DeleteUser(db))
			admin.POST("/users/:id/reset-password", handlers.ResetUserPassword(db))
			admin.GET("/settings/scrape-delay", handlers.GetScrapeDelay(db, cm))
			admin.PUT("/settings/scrape-delay", handlers.SetScrapeDelay(db, cm))
		}

		// Protected — accepts API key OR admin session
		protected := api.Group("/")
		protected.Use(middleware.APIKeyOrAdminSession())
		{
			protected.POST("/wrestlers", handlers.CreateWrestler(db))
			protected.PUT("/wrestlers/:id", handlers.UpdateWrestler(db))
			protected.DELETE("/wrestlers/:id", handlers.DeleteWrestler(db))
			protected.POST("/matches", handlers.CreateMatch(db))
			protected.DELETE("/matches/:id", handlers.DeleteMatch(db))

			// Scraper controls — all manual
			protected.POST("/scraper/collect", handleCollect(cm, proc))
			protected.POST("/scraper/collect/titles", handleCollectTitles(cm))
			protected.POST("/scraper/collect/titles/bytitle", handleCollectTitlesByTitle(cm, proc))
			protected.POST("/scraper/collect/promotions", handleCollectPromotions(cm, proc))
			protected.POST("/scraper/regions", handleRegions(proc))
			protected.POST("/scraper/validate", handleValidate(cm, proc))
			protected.POST("/scraper/recalculate", handleRecalculate(proc))
			protected.POST("/scraper/refresh-profiles", handleRefreshProfiles(cm, proc))
			protected.POST("/scraper/cron/start", handleCronStart(cm, proc))
			protected.POST("/scraper/cron/stop", handleCronStop())
			protected.POST("/scraper/run/matches", handleRunMatchScrape(cm, proc))
			protected.POST("/scraper/run/profiles", handleRunProfileRefresh(cm, proc))
			protected.GET("/scraper/status", handleScraperStatus(cm))
			// tasklog moved to public api group

			// Test scrape endpoints
			protected.POST("/scraper/test-clear", handleTestClear(db))
			protected.POST("/scraper/test-scrape", handleTestScrape(cm, proc, db))
			protected.POST("/scraper/test-validate", handleTestValidate(cm, db))
			protected.GET("/scraper/test-scrape/results", handleTestScrapeResults())
			protected.GET("/scraper/test-validate/results", handleTestValidateResults())

			// PFP update from Twitter
			protected.POST("/scraper/pfp-update", handlePfpUpdate(db))
			protected.GET("/scraper/pfp-update/status", handlePfpUpdateStatus())
		}
	}

	log.Printf("🏆 Joshitori API starting on port %s", port)
	log.Println("🕷️ Scraper is NOT running — use POST /api/scraper/collect to trigger")

	// Warm expensive caches in background so first visitors don't wait
	handlers.WarmRecordsCache(db)

	r.Run(":" + port)
}

// --- Scraper Handlers ---

// POST /api/scraper/collect
// Scrapes all tracked wrestlers for matches, stores them WITHOUT ELO.
// Safe to run multiple times — skips matches we already have.
// Use this to build up the dataset over multiple runs.
// POST /api/scraper/refresh-profiles
// Re-scrapes all wrestler profiles to catch changes (promotion switches, name changes, etc.)
func handleRefreshProfiles(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		go func() {
			log.Println("👤 Profile refresh starting...")
			start := time.Now()

			updated, err := cm.RefreshProfiles(proc)
			if err != nil {
				log.Printf("👤 Profile refresh error: %v", err)
				return
			}

			log.Printf("👤 Profile refresh complete — %d wrestlers updated in %s", updated, time.Since(start))
		}()

		c.JSON(http.StatusAccepted, gin.H{
			"message": "Profile refresh started — check logs for progress",
		})
	}
}

func handleCollect(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		go func() {
			log.Println("🕷️ Collection run starting (full crawl with discovery)...")
			start := time.Now()

			// FetchAndCollect loops: scrape → process → discover new wrestlers → repeat
			newCount, err := cm.FetchAndCollect(proc)
			if err != nil {
				log.Printf("🕷️ Scraper error: %v", err)
				return
			}

			lastScrapeTime = time.Now()
			lastScrapeMatches = newCount

			handlers.InvalidateRecordsCache()
			log.Printf("🕷️ Collection complete — %d new matches in %s", newCount, time.Since(start))
		}()

		c.JSON(http.StatusAccepted, gin.H{
			"message": "Collection started (full crawl with discovery) — check logs for progress",
		})
	}
}

// POST /api/scraper/collect/titles
func handleCollectTitles(cm *scraper.CagematchScraper) gin.HandlerFunc {
	return func(c *gin.Context) {
		go func() {
			log.Println("🏆 Title reign collection starting...")
			start := time.Now()

			var wrestlers []models.Wrestler
			cm.DB().Where("cagematch_id > 0").Find(&wrestlers)

			count := 0
			for i, w := range wrestlers {
				cm.SetStatus(fmt.Sprintf("Titles: %s (%d/%d)", w.Name, i+1, len(wrestlers)), true)

				entries, err := cm.ScrapeTitleReigns(int(w.CagematchID))
				if err != nil {
					log.Printf("[titles] Error for %s: %v", w.Name, err)
					continue
				}
				for _, te := range entries {
					tr := models.TitleReign{
						WrestlerID:       w.ID,
						TitleName:        te.TitleName,
						CagematchTitleID: te.CagematchTitleID,
						ReignNumber:      te.ReignNumber,
						WonDate:          te.WonDate,
						LostDate:         te.LostDate,
						DurationDays:     te.DurationDays,
					}
					cm.DB().Where("wrestler_id = ? AND cagematch_title_id = ? AND won_date = ?", w.ID, te.CagematchTitleID, te.WonDate).
						Assign(tr).FirstOrCreate(&tr)
					count++
				}
				time.Sleep(cm.GetDelay())
			}

			cm.SetStatus("", false)
			log.Printf("🏆 Title collection complete — %d reigns in %s", count, time.Since(start))
		}()

		c.JSON(http.StatusAccepted, gin.H{"message": "Title reign collection started"})
	}
}

// POST /api/scraper/collect/titles/bytitle
// Scrapes title histories per-title instead of per-wrestler. Much more efficient.
// Discovers new titles, skips titles with no female holders, marks male holders as untracked.
func handleCollectTitlesByTitle(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		go func() {
			log.Println("🏆 Title history collection (per-title) starting...")
			start := time.Now()

			count, err := cm.CollectTitleHistories(proc)
			if err != nil {
				log.Printf("🏆 Title history error: %v", err)
				return
			}

			log.Printf("🏆 Title history complete — %d reigns in %s", count, time.Since(start))
		}()

		c.JSON(http.StatusAccepted, gin.H{"message": "Title history collection (per-title) started — check logs"})
	}
}

// POST /api/scraper/collect/promotions
func handleCollectPromotions(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		go func() {
			log.Println("🏢 Promotion history collection starting...")
			start := time.Now()

			var wrestlers []models.Wrestler
			cm.DB().Where("cagematch_id > 0").Find(&wrestlers)

			count := 0
			for i, w := range wrestlers {
				cm.SetStatus(fmt.Sprintf("Promotions: %s (%d/%d)", w.Name, i+1, len(wrestlers)), true)

				entries, err := cm.ScrapePromotionHistory(int(w.CagematchID))
				if err != nil {
					log.Printf("[promotions] Error for %s: %v", w.Name, err)
					continue
				}
				for _, pe := range entries {
					ph := models.PromotionHistory{
						WrestlerID:  w.ID,
						Promotion:   pe.Promotion,
						PromotionID: pe.PromotionID,
						Year:        pe.Year,
						Matches:     pe.Matches,
					}
					cm.DB().Where("wrestler_id = ? AND promotion = ? AND year = ?", w.ID, pe.Promotion, pe.Year).
						Assign(ph).FirstOrCreate(&ph)
					count++
				}
				// Update promotion using tiered logic (last 50 matches)
				if len(entries) > 0 {
					newPromo := proc.DetermineCurrentPromotion(w.ID)
					if newPromo != "" {
						cm.DB().Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("promotion", newPromo)
					}
				}
				time.Sleep(cm.GetDelay())
			}

			cm.SetStatus("", false)
			log.Printf("🏢 Promotion history complete — %d entries in %s", count, time.Since(start))
		}()

		c.JSON(http.StatusAccepted, gin.H{"message": "Promotion history collection started"})
	}
}

// POST /api/scraper/regions
func handleRegions(proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		go proc.UpdateWrestlerRegions()
		c.JSON(http.StatusAccepted, gin.H{"message": "Region calculation started"})
	}
}

// POST /api/scraper/recalculate
// Resets all wrestlers to 1200 and replays ALL matches chronologically.
// Run this AFTER you're happy with the collected dataset.
// POST /api/scraper/validate
// Compares promotion_histories (CM expected counts) vs actual DB match counts.
// Re-scrapes any wrestlers with missing matches.
func handleValidate(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		go func() {
			log.Println("🔍 Validation run starting...")
			start := time.Now()

			newMatches, rescrapeCount, err := cm.ValidateAndRescrape(proc)
			if err != nil {
				log.Printf("🔍 Validation error: %v", err)
				return
			}

			log.Printf("🔍 Validation complete — re-scraped %d wrestlers, added %d new matches in %s",
				rescrapeCount, newMatches, time.Since(start))
		}()

		c.JSON(http.StatusAccepted, gin.H{
			"message": "Validation started — comparing CM expected counts vs DB, re-scraping mismatches. Check logs for progress.",
		})
	}
}

func handleRecalculate(proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		tasklog.Clear()
		tasklog.Info("ELO recalculation starting...")

		go func() {
			if err := proc.RecalculateAllELO(); err != nil {
				tasklog.Errorf("ELO recalculation error: %v", err)
				log.Printf("🕷️ ELO recalculation error: %v", err)
			} else {
				tasklog.Success("ELO recalculation complete!")
			}
			handlers.InvalidateRecordsCache()
		}()

		c.JSON(http.StatusAccepted, gin.H{
			"message": "ELO recalculation started",
		})
	}
}

// GET /api/tasklog?since=N
// Returns task log entries since index N. Poll this for live progress.
func handleTaskLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		since := 0
		if s := c.Query("since"); s != "" {
			fmt.Sscanf(s, "%d", &since)
		}
		entries, next := tasklog.GetSince(since)
		c.JSON(http.StatusOK, gin.H{
			"entries": entries,
			"next":    next,
		})
	}
}

// POST /api/scraper/cron/start
// Starts the scheduler with two jobs:
//   - Match scrape: runs once per day (24h)
//   - Profile refresh: runs once per week (168h)
func handleCronStart(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		cronMu.Lock()
		defer cronMu.Unlock()

		if cronRunning {
			c.JSON(http.StatusConflict, gin.H{"message": "Cron already running"})
			return
		}

		cronStop = make(chan struct{})
		cronRunning = true

		go func() {
			matchInterval := 24 * time.Hour
			profileInterval := 7 * 24 * time.Hour

			matchTicker := time.NewTicker(matchInterval)
			profileTicker := time.NewTicker(profileInterval)
			defer matchTicker.Stop()
			defer profileTicker.Stop()

			nextMatchScrape = time.Now().Add(matchInterval)
			nextProfileRefresh = time.Now().Add(profileInterval)

			log.Printf("🕷️ Cron started — matches every %s, profiles every %s", matchInterval, profileInterval)

			for {
				select {
				case <-matchTicker.C:
					runMatchScrape(cm, proc)
					nextMatchScrape = time.Now().Add(matchInterval)

				case <-profileTicker.C:
					runProfileRefresh(cm, proc)
					nextProfileRefresh = time.Now().Add(profileInterval)

				case <-cronStop:
					log.Println("🕷️ Cron stopped")
					return
				}
			}
		}()

		c.JSON(http.StatusOK, gin.H{
			"message":          "Cron started — matches daily, profiles weekly",
			"match_interval":   "24h",
			"profile_interval": "168h",
		})
	}
}

// runMatchScrape executes the incremental match scrape (used by cron and manual trigger)
func runMatchScrape(cm *scraper.CagematchScraper, proc *scraper.Processor) {
	cronMu.Lock()
	if matchScrapeRunning {
		cronMu.Unlock()
		log.Println("🕷️ Match scrape already running — skipping")
		return
	}
	matchScrapeRunning = true
	cronMu.Unlock()

	defer func() {
		cronMu.Lock()
		matchScrapeRunning = false
		cronMu.Unlock()
	}()

	log.Println("🕷️ Match scrape starting...")
	start := time.Now()

	newCount, err := cm.IncrementalFetchAndCollect(proc)
	if err != nil {
		log.Printf("🕷️ Match scrape error: %v", err)
		return
	}

	cronMu.Lock()
	lastMatchScrapeTime = time.Now()
	lastMatchScrapeMatches = newCount
	lastScrapeTime = time.Now()
	lastScrapeMatches = newCount
	cronMu.Unlock()

	handlers.InvalidateRecordsCache()
	log.Printf("🕷️ Match scrape complete — %d new matches in %s", newCount, time.Since(start))
}

// runProfileRefresh executes the profile refresh (used by cron and manual trigger)
func runProfileRefresh(cm *scraper.CagematchScraper, proc *scraper.Processor) {
	cronMu.Lock()
	if profileRefreshRunning {
		cronMu.Unlock()
		log.Println("👤 Profile refresh already running — skipping")
		return
	}
	profileRefreshRunning = true
	cronMu.Unlock()

	defer func() {
		cronMu.Lock()
		profileRefreshRunning = false
		cronMu.Unlock()
	}()

	log.Println("👤 Profile refresh starting...")
	start := time.Now()

	updated, err := cm.RefreshProfiles(proc)
	if err != nil {
		log.Printf("👤 Profile refresh error: %v", err)
		return
	}

	cronMu.Lock()
	lastProfileRefreshTime = time.Now()
	lastProfileRefreshCount = updated
	cronMu.Unlock()

	log.Printf("👤 Profile refresh complete — %d wrestlers updated in %s", updated, time.Since(start))
}

// POST /api/scraper/run/matches — manual trigger for match scrape
func handleRunMatchScrape(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		cronMu.Lock()
		if matchScrapeRunning {
			cronMu.Unlock()
			c.JSON(http.StatusConflict, gin.H{"message": "Match scrape already running"})
			return
		}
		cronMu.Unlock()

		go runMatchScrape(cm, proc)

		c.JSON(http.StatusAccepted, gin.H{
			"message": "Match scrape started — check /api/scraper/status to monitor",
		})
	}
}

// POST /api/scraper/run/profiles — manual trigger for profile refresh
func handleRunProfileRefresh(cm *scraper.CagematchScraper, proc *scraper.Processor) gin.HandlerFunc {
	return func(c *gin.Context) {
		cronMu.Lock()
		if profileRefreshRunning {
			cronMu.Unlock()
			c.JSON(http.StatusConflict, gin.H{"message": "Profile refresh already running"})
			return
		}
		cronMu.Unlock()

		go runProfileRefresh(cm, proc)

		c.JSON(http.StatusAccepted, gin.H{
			"message": "Profile refresh started — check /api/scraper/status to monitor",
		})
	}
}

// POST /api/scraper/cron/stop
func handleCronStop() gin.HandlerFunc {
	return func(c *gin.Context) {
		cronMu.Lock()
		defer cronMu.Unlock()

		if !cronRunning {
			c.JSON(http.StatusConflict, gin.H{"message": "Cron not running"})
			return
		}

		close(cronStop)
		cronRunning = false

		c.JSON(http.StatusOK, gin.H{"message": "Cron stopped"})
	}
}

// GET /api/scraper/status
// Returns full status of both scrapers with live monitoring data.
func handleScraperStatus(cm *scraper.CagematchScraper) gin.HandlerFunc {
	return func(c *gin.Context) {
		cronMu.Lock()
		cron := cronRunning
		matchRunning := matchScrapeRunning
		profileRunning := profileRefreshRunning
		lastMatchTime := lastMatchScrapeTime
		lastMatchCount := lastMatchScrapeMatches
		lastProfileTime := lastProfileRefreshTime
		lastProfileCount := lastProfileRefreshCount
		nextMatch := nextMatchScrape
		nextProfile := nextProfileRefresh
		cronMu.Unlock()

		currentWrestler, isRunning := cm.GetStatus()

		// Build status response
		status := gin.H{
			"cron_running":     cron,
			"is_running":       isRunning,
			"current_activity": currentWrestler,
			"match_scraper": gin.H{
				"running":       matchRunning,
				"last_run":      nil,
				"matches_found": lastMatchCount,
				"next_run":      nil,
			},
			"profile_refresher": gin.H{
				"running":          profileRunning,
				"last_run":         nil,
				"wrestlers_updated": lastProfileCount,
				"next_run":         nil,
			},
		}

		// Fill in times if set
		matchStatus := status["match_scraper"].(gin.H)
		if !lastMatchTime.IsZero() {
			matchStatus["last_run"] = lastMatchTime.Format(time.RFC3339)
		}
		if cron && !nextMatch.IsZero() {
			matchStatus["next_run"] = nextMatch.Format(time.RFC3339)
		}

		profileStatus := status["profile_refresher"].(gin.H)
		if !lastProfileTime.IsZero() {
			profileStatus["last_run"] = lastProfileTime.Format(time.RFC3339)
		}
		if cron && !nextProfile.IsZero() {
			profileStatus["next_run"] = nextProfile.Format(time.RFC3339)
		}

		// Legacy fields for backwards compat
		if !lastScrapeTime.IsZero() {
			status["last_run"] = lastScrapeTime.Format(time.RFC3339)
		}
		status["matches_found"] = lastScrapeMatches

		c.JSON(http.StatusOK, status)
	}
}

// --- Test Scrape Handlers ---

func handleTestClear(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var matchCount int64
		db.Model(&models.Match{}).Count(&matchCount)

		db.Exec("DELETE FROM momentum_histories")
		db.Exec("DELETE FROM elo_histories")
		db.Exec("DELETE FROM match_participants")
		db.Exec("DELETE FROM matches")

		c.JSON(http.StatusOK, gin.H{
			"message":         fmt.Sprintf("Cleared %d matches and all related data", matchCount),
			"matches_cleared": matchCount,
		})
	}
}

func handleTestScrape(cm *scraper.CagematchScraper, proc *scraper.Processor, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			WrestlerIDs []uint `json:"wrestler_ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Expected {\"wrestler_ids\": [1,2,3]}"})
			return
		}

		var wrestlers []models.Wrestler
		db.Where("id IN ?", req.WrestlerIDs).Find(&wrestlers)
		if len(wrestlers) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No wrestlers found for given IDs"})
			return
		}

		testMu.Lock()
		testScrapeResults = nil
		testMu.Unlock()

		go func() {
			for _, w := range wrestlers {
				cm.SetStatus(fmt.Sprintf("[test] %s (CM#%d)", w.Name, w.CagematchID), true)
				log.Printf("[test-scrape] Scraping %s (CM#%d)...", w.Name, w.CagematchID)

				matches, err := cm.TestScrapeWrestler(int(w.CagematchID))
				if err != nil {
					log.Printf("[test-scrape] Error scraping %s: %v", w.Name, err)
					testMu.Lock()
					testScrapeResults = append(testScrapeResults, gin.H{
						"wrestler_id":  w.ID,
						"name":         w.Name,
						"cagematch_id": w.CagematchID,
						"error":        err.Error(),
					})
					testMu.Unlock()
					continue
				}

				newCount := proc.CollectMatches(matches)

				var dbCount int64
				db.Model(&models.MatchParticipant{}).Where("wrestler_id = ?", w.ID).Count(&dbCount)

				result := gin.H{
					"wrestler_id":     w.ID,
					"name":            w.Name,
					"cagematch_id":    w.CagematchID,
					"scraped_from_cm": len(matches),
					"new_stored":      newCount,
					"total_in_db":     dbCount,
				}
				log.Printf("[test-scrape] %s: %d from CM, %d new, %d total in DB", w.Name, len(matches), newCount, dbCount)

				testMu.Lock()
				testScrapeResults = append(testScrapeResults, result)
				testMu.Unlock()
			}
			cm.SetStatus("", false)
			log.Println("[test-scrape] Complete!")
		}()

		names := make([]string, len(wrestlers))
		for i, w := range wrestlers {
			names[i] = w.Name
		}
		c.JSON(http.StatusAccepted, gin.H{
			"message": fmt.Sprintf("Test scrape started for %d wrestlers: %v", len(wrestlers), names),
		})
	}
}

func handleTestValidate(cm *scraper.CagematchScraper, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			WrestlerIDs []uint `json:"wrestler_ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Expected {\"wrestler_ids\": [1,2,3]}"})
			return
		}

		var wrestlers []models.Wrestler
		db.Where("id IN ?", req.WrestlerIDs).Find(&wrestlers)
		if len(wrestlers) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No wrestlers found"})
			return
		}

		testMu.Lock()
		testValidateResults = nil
		testMu.Unlock()

		go func() {
			for _, w := range wrestlers {
				cm.SetStatus(fmt.Sprintf("[validate] %s", w.Name), true)
				log.Printf("[test-validate] Checking %s (CM#%d)...", w.Name, w.CagematchID)

				matches, err := cm.TestScrapeWrestler(int(w.CagematchID))
				cmCount := 0
				if err != nil {
					log.Printf("[test-validate] Error scraping %s: %v", w.Name, err)
				} else {
					cmCount = len(matches)
				}

				var dbCount int64
				db.Model(&models.MatchParticipant{}).Where("wrestler_id = ?", w.ID).Count(&dbCount)

				var phExpected int64
				db.Model(&models.PromotionHistory{}).Select("COALESCE(SUM(matches), 0)").Where("wrestler_id = ?", w.ID).Scan(&phExpected)

				result := gin.H{
					"wrestler_id":    w.ID,
					"name":           w.Name,
					"cagematch_id":   w.CagematchID,
					"cm_match_count": cmCount,
					"db_match_count": dbCount,
					"promo_hist_sum": phExpected,
					"diff_cm_vs_db":  cmCount - int(dbCount),
					"diff_ph_vs_db":  int(phExpected) - int(dbCount),
					"match_rate":     "",
				}
				if cmCount > 0 {
					rate := float64(dbCount) / float64(cmCount) * 100
					result["match_rate"] = fmt.Sprintf("%.1f%%", rate)
				}

				log.Printf("[test-validate] %s: CM=%d, DB=%d, PH=%d, diff=%d",
					w.Name, cmCount, dbCount, phExpected, cmCount-int(dbCount))

				testMu.Lock()
				testValidateResults = append(testValidateResults, result)
				testMu.Unlock()
			}
			cm.SetStatus("", false)
			log.Println("[test-validate] Complete!")
		}()

		c.JSON(http.StatusAccepted, gin.H{
			"message": fmt.Sprintf("Validation started for %d wrestlers", len(wrestlers)),
		})
	}
}

func handleTestScrapeResults() gin.HandlerFunc {
	return func(c *gin.Context) {
		testMu.Lock()
		defer testMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"results": testScrapeResults})
	}
}

func handleTestValidateResults() gin.HandlerFunc {
	return func(c *gin.Context) {
		testMu.Lock()
		defer testMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"results": testValidateResults})
	}
}

// --- PFP Update from Twitter ---

var screenNameRe = regexp.MustCompile(`(?:twitter\.com|x\.com)/([A-Za-z0-9_]+)`)

func extractScreenName(url string) string {
	m := screenNameRe.FindStringSubmatch(url)
	if m == nil {
		return ""
	}
	name := m[1]
	lower := strings.ToLower(name)
	if lower == "intent" || lower == "i" || lower == "search" || lower == "hashtag" || lower == "home" {
		return ""
	}
	return name
}

func fetchTwitterProfileImage(screenName string) (string, error) {
	url := fmt.Sprintf("https://api.fxtwitter.com/%s", screenName)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return "", fmt.Errorf("account closed/suspended (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	// Extract avatar_url from fxtwitter JSON response
	re := regexp.MustCompile(`"avatar_url":"([^"]+)"`)
	match := re.FindStringSubmatch(string(body))
	if match == nil {
		return "", fmt.Errorf("no avatar found (account may be closed)")
	}

	imageURL := strings.Replace(match[1], "_normal.", "_400x400.", 1)
	return imageURL, nil
}

func downloadWrestlerImage(imageURL string, wrestlerID int) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(imageURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	ext := ".jpg"
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "png") {
		ext = ".png"
	} else if strings.Contains(ct, "webp") {
		ext = ".webp"
	}

	filename := fmt.Sprintf("%d%s", wrestlerID, ext)
	diskPath := filepath.Join("static", "images", "wrestlers", filename)
	os.MkdirAll(filepath.Dir(diskPath), 0755)

	f, err := os.Create(diskPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	return "/static/images/wrestlers/" + filename, nil
}

func handlePfpUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		pfpMu.Lock()
		if pfpRunning {
			pfpMu.Unlock()
			c.JSON(http.StatusConflict, gin.H{"error": "PFP update already running"})
			return
		}
		pfpRunning = true
		pfpTotal = 0
		pfpProcessed = 0
		pfpSuccess = 0
		pfpClosed = 0
		pfpFailed = 0
		pfpLog = nil
		pfpMu.Unlock()

		// Find candidates: wrestlers without images who have twitter links
		type candidate struct {
			WrestlerID int
			Name       string
			TwitterURL string
		}
		var candidates []candidate
		db.Raw(`
			SELECT w.id as wrestler_id, w.name, s.url as twitter_url
			FROM wrestlers w
			JOIN socials s ON s.wrestler_id = w.id AND s.name = 'twitter'
			WHERE (w.image_url IS NULL OR w.image_url = '')
			  AND (w.image_local IS NULL OR w.image_local = '')
			ORDER BY w.id
		`).Scan(&candidates)

		pfpMu.Lock()
		pfpTotal = len(candidates)
		pfpMu.Unlock()

		username, _ := c.Get("username")
		log.Printf("[pfp] %s started PFP update — %d candidates", username, len(candidates))

		c.JSON(http.StatusOK, gin.H{
			"message":    fmt.Sprintf("PFP update started — %d wrestlers to process", len(candidates)),
			"candidates": len(candidates),
		})

		// Run in background
		go func() {
			defer func() {
				pfpMu.Lock()
				pfpRunning = false
				pfpMu.Unlock()
				log.Printf("[pfp] Complete — success: %d, closed: %d, failed: %d", pfpSuccess, pfpClosed, pfpFailed)
			}()

			for _, cand := range candidates {
				sn := extractScreenName(cand.TwitterURL)
				if sn == "" {
					pfpMu.Lock()
					pfpProcessed++
					pfpFailed++
					pfpLog = append(pfpLog, gin.H{"name": cand.Name, "status": "skip", "detail": "bad twitter URL"})
					pfpMu.Unlock()
					continue
				}

				imageURL, err := fetchTwitterProfileImage(sn)
				if err != nil {
					errStr := err.Error()
					pfpMu.Lock()
					pfpProcessed++
					if strings.Contains(errStr, "closed") || strings.Contains(errStr, "suspended") || strings.Contains(errStr, "HTTP 4") {
						pfpClosed++
						pfpLog = append(pfpLog, gin.H{"name": cand.Name, "status": "closed", "detail": "@" + sn})
					} else {
						pfpFailed++
						pfpLog = append(pfpLog, gin.H{"name": cand.Name, "status": "error", "detail": errStr})
					}
					pfpMu.Unlock()
					time.Sleep(500 * time.Millisecond)
					continue
				}

				localPath, err := downloadWrestlerImage(imageURL, cand.WrestlerID)
				if err != nil {
					pfpMu.Lock()
					pfpProcessed++
					pfpFailed++
					pfpLog = append(pfpLog, gin.H{"name": cand.Name, "status": "error", "detail": "download: " + err.Error()})
					pfpMu.Unlock()
					time.Sleep(500 * time.Millisecond)
					continue
				}

				db.Model(&models.Wrestler{}).Where("id = ?", cand.WrestlerID).Updates(map[string]interface{}{
					"image_url":   imageURL,
					"image_local": localPath,
				})

				pfpMu.Lock()
				pfpProcessed++
				pfpSuccess++
				pfpLog = append(pfpLog, gin.H{"name": cand.Name, "status": "ok", "detail": "@" + sn + " → " + localPath})
				pfpMu.Unlock()

				time.Sleep(1 * time.Second)
			}
		}()
	}
}

func handlePfpUpdateStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		pfpMu.Lock()
		defer pfpMu.Unlock()

		// Return last 50 log entries
		logSlice := pfpLog
		if len(logSlice) > 50 {
			logSlice = logSlice[len(logSlice)-50:]
		}

		c.JSON(http.StatusOK, gin.H{
			"running":   pfpRunning,
			"total":     pfpTotal,
			"processed": pfpProcessed,
			"success":   pfpSuccess,
			"closed":    pfpClosed,
			"failed":    pfpFailed,
			"log":       logSlice,
		})
	}
}
