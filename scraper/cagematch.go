package scraper

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

// CagematchScraper implements the ResultsScraper interface.
// It scrapes wrestler profiles and match histories from cagematch.net.
//
// Think of this like a Python class:
//   class CagematchScraper:
//       def __init__(self, db):
//           self.db = db
//           self.skip_list = set()  # male wrestler IDs
//
// In Go, we use a struct + methods instead of a class.
type CagematchScraper struct {
	db              *gorm.DB
	baseURL         string
	skipList        map[int]bool   // cagematch IDs we know are male — don't re-fetch
	mu              sync.Mutex     // protects skipList from concurrent access
	delay           time.Duration  // delay between HTTP requests (be respectful!)
	CurrentWrestler string         // name of wrestler currently being scraped
	IsRunning       bool           // whether a scrape is in progress
}

// GetDelay returns the scraper's configured delay between requests
func (s *CagematchScraper) GetDelay() time.Duration {
	return s.delay
}

// SetDelay updates the scraper's delay between requests
func (s *CagematchScraper) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// DB returns the scraper's database connection
func (s *CagematchScraper) DB() *gorm.DB {
	return s.db
}

// SetStatus updates the scraper's current status display
func (s *CagematchScraper) SetStatus(wrestler string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentWrestler = wrestler
	s.IsRunning = running
}

// NewCagematchScraper creates a new scraper instance.
// This is Go's version of __init__ / a constructor.
func NewCagematchScraper(db *gorm.DB) *CagematchScraper {
	s := &CagematchScraper{
		db:       db,
		baseURL:  "https://www.cagematch.net",
		skipList: make(map[int]bool),
		delay:    527 * time.Second, // respectful default; override via admin settings
	}

	// Load persisted skip list from DB
	var skipped []models.SkippedWrestler
	db.Find(&skipped)
	for _, sw := range skipped {
		s.skipList[sw.CagematchID] = true
	}
	log.Printf("[cagematch] Loaded %d skipped (male) wrestlers from DB", len(skipped))

	return s
}

// Name satisfies the ResultsScraper interface.
func (s *CagematchScraper) Name() string {
	return "cagematch"
}

// FetchResults satisfies the ResultsScraper interface.
// This is the main entry point — called by the scheduler.
//
// Strategy:
// 1. Get all wrestlers already in our DB that have a CagematchID
// 2. Scrape each wrestler's match history for new matches
// 3. Discover new female wrestlers along the way
// 4. Return all new matches found
func (s *CagematchScraper) FetchResults() ([]RawMatch, error) {
	// Get all wrestlers we're tracking
	var wrestlers []models.Wrestler
	s.db.Where("cagematch_id > 0").Find(&wrestlers)

	if len(wrestlers) == 0 {
		log.Println("[cagematch] No wrestlers with CagematchID found. Seed some first!")
		return nil, nil
	}

	var allMatches []RawMatch

	for _, w := range wrestlers {
		log.Printf("[cagematch] Scraping FULL match history for %s (CM#%d)", w.Name, w.CagematchID)

		matches, err := s.scrapeWrestlerMatches(int(w.CagematchID))
		if err != nil {
			log.Printf("[cagematch] Error scraping %s: %v", w.Name, err)
			continue
		}

		allMatches = append(allMatches, matches...)
		time.Sleep(s.delay)
	}

	// Deduplicate — same match appears in multiple wrestlers' histories
	allMatches = deduplicateMatches(allMatches)

	log.Printf("[cagematch] Found %d unique matches total", len(allMatches))
	return allMatches, nil
}

// EnrichWrestlers scrapes profiles for wrestlers that are missing data.
// This fills in seed wrestlers and any that had parsing issues.
func (s *CagematchScraper) EnrichWrestlers() {
	var wrestlers []models.Wrestler
	// Find wrestlers with CagematchID but missing profile data
	s.db.Where("cagematch_id > 0 AND (birthplace = '' OR birthplace IS NULL)").Find(&wrestlers)

	if len(wrestlers) == 0 {
		return
	}

	log.Printf("[cagematch] Enriching %d wrestlers with profile data...", len(wrestlers))

	for _, w := range wrestlers {
		profile, err := s.ScrapeWrestlerProfile(int(w.CagematchID))
		if err != nil || profile == nil {
			continue
		}

		// Update the wrestler with profile data
		updates := map[string]interface{}{
			"birthday":        profile.Birthday,
			"birthplace":      profile.Birthplace,
			"height":          profile.Height,
			"weight":          profile.Weight,
			"debut_year":      profile.DebutYear,
			"wrestling_style": profile.WrestlingStyle,
		}
		// Always update name to current gimmick from profile
		if profile.Name != "" {
			updates["name"] = profile.Name
		}
		// Only update promotion if current is from seed (basic) and profile has one
		if profile.Promotion != "" {
			updates["promotion"] = profile.Promotion
		}

		s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Updates(updates)

		// Add aliases if we don't have any
		var aliasCount int64
		s.db.Model(&models.WrestlerAlias{}).Where("wrestler_id = ?", w.ID).Count(&aliasCount)
		if aliasCount == 0 {
			for _, alias := range profile.AlterEgos {
				s.db.Create(&models.WrestlerAlias{WrestlerID: w.ID, Alias: alias})
			}
		}

		// Add socials if we don't have any
		var socialCount int64
		s.db.Model(&models.Socials{}).Where("wrestler_id = ?", w.ID).Count(&socialCount)
		if socialCount == 0 {
			for name, url := range profile.Socials {
				s.db.Create(&models.Socials{WrestlerID: w.ID, Name: name, URL: url})
			}
		}

		log.Printf("[cagematch] Enriched: %s", profile.Name)
		time.Sleep(s.delay)
	}
}

// FetchAndCollect combines fetching and collecting in a loop.
// It scrapes all known wrestlers, processes matches (discovering new wrestlers),
// then repeats for newly discovered wrestlers until no new ones are found.
// This is the method to use for initial collection.
func (s *CagematchScraper) FetchAndCollect(proc *Processor) (int, error) {
	// First, enrich any wrestlers missing profile data (seeds, failed parses)
	s.EnrichWrestlers()

	scraped := make(map[uint]bool)
	totalNew := 0

	for pass := 1; ; pass++ {
		// Get all wrestlers with CagematchIDs (including newly discovered)
		var wrestlers []models.Wrestler
		s.db.Where("cagematch_id > 0").Find(&wrestlers)

		// Find unscraped wrestlers (skip recently scraped ones)
		cutoff := time.Now().Add(-24 * time.Hour)
		var toScrape []models.Wrestler
		for _, w := range wrestlers {
			if scraped[w.CagematchID] || w.CagematchID == 0 {
				continue
			}
			// Skip if scraped within last 24 hours
			if w.LastScrapedAt != nil && w.LastScrapedAt.After(cutoff) {
				scraped[w.CagematchID] = true // mark so we don't re-check
				continue
			}
			toScrape = append(toScrape, w)
		}

		if len(toScrape) == 0 {
			log.Printf("[cagematch] All wrestlers scraped after %d passes", pass-1)
			break
		}

		log.Printf("[cagematch] === Pass %d: %d wrestlers to scrape ===", pass, len(toScrape))

		for i, w := range toScrape {
			s.mu.Lock()
			s.CurrentWrestler = fmt.Sprintf("%s (%d/%d, pass %d)", w.Name, i+1, len(toScrape), pass)
			s.IsRunning = true
			s.mu.Unlock()

			log.Printf("[cagematch] Scraping FULL history for %s (CM#%d)", w.Name, w.CagematchID)
			scraped[w.CagematchID] = true

			matches, err := s.scrapeWrestlerMatches(int(w.CagematchID))
			if err != nil {
				log.Printf("[cagematch] Error scraping %s: %v", w.Name, err)
				continue
			}

			// Process matches immediately — this discovers new wrestlers
			matches = deduplicateMatches(matches)
			newCount := proc.CollectMatches(matches)
			totalNew += newCount

			// Scrape promotion history (page=20)
			promoEntries, promoErr := s.ScrapePromotionHistory(int(w.CagematchID))
			if promoErr != nil {
				log.Printf("[cagematch] Error scraping promo history for %s: %v", w.Name, promoErr)
			} else if len(promoEntries) > 0 {
				// Upsert promotion history entries
				for _, pe := range promoEntries {
					ph := models.PromotionHistory{
						WrestlerID:  w.ID,
						Promotion:   pe.Promotion,
						PromotionID: pe.PromotionID,
						Year:        pe.Year,
						Matches:     pe.Matches,
					}
					s.db.Where("wrestler_id = ? AND promotion = ? AND year = ?", w.ID, pe.Promotion, pe.Year).
						Assign(ph).FirstOrCreate(&ph)
				}
				// Update wrestler's current promotion using tiered logic
				newPromo := proc.DetermineCurrentPromotion(w.ID)
				if newPromo != "" {
					s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("promotion", newPromo)
				}
				log.Printf("[cagematch] %s: %d promotion history entries", w.Name, len(promoEntries))

				// Scrape any unknown promotions
				for _, pe := range promoEntries {
					if pe.PromotionID == 0 {
						continue
					}
					var existing models.Promotion
					if s.db.Where("cagematch_id = ?", pe.PromotionID).First(&existing).Error == nil {
						continue // already have it
					}
					promoData, err := s.ScrapePromotion(pe.PromotionID)
					if err != nil {
						log.Printf("[cagematch] Error scraping promotion %d: %v", pe.PromotionID, err)
						continue
					}
					s.db.Create(promoData)
					log.Printf("[cagematch] Scraped promotion: %s (%s)", promoData.Name, promoData.Country)
					time.Sleep(s.delay)
				}
			}
			// Scrape title reigns (page=11)
			titleEntries, titleErr := s.ScrapeTitleReigns(int(w.CagematchID))
			if titleErr != nil {
				log.Printf("[cagematch] Error scraping titles for %s: %v", w.Name, titleErr)
			} else if len(titleEntries) > 0 {
				for _, te := range titleEntries {
					tr := models.TitleReign{
						WrestlerID:       w.ID,
						TitleName:        te.TitleName,
						CagematchTitleID: te.CagematchTitleID,
						ReignNumber:      te.ReignNumber,
						WonDate:          te.WonDate,
						LostDate:         te.LostDate,
						DurationDays:     te.DurationDays,
					}
					s.db.Where("wrestler_id = ? AND cagematch_title_id = ? AND won_date = ?", w.ID, te.CagematchTitleID, te.WonDate).
						Assign(tr).FirstOrCreate(&tr)
				}
				log.Printf("[cagematch] %s: %d title reigns", w.Name, len(titleEntries))
				s.syncTitleReigns(titleEntries, proc)
			}
			time.Sleep(s.delay)

			// Mark wrestler as scraped with timestamp
			now := time.Now()
			s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("last_scraped_at", now)

			log.Printf("[cagematch] %s: %d matches scraped, %d new stored", w.Name, len(matches), newCount)
			time.Sleep(s.delay)
		}
	}

	s.mu.Lock()
	s.CurrentWrestler = ""
	s.IsRunning = false
	s.mu.Unlock()

	return totalNew, nil
}

// IncrementalFetchAndCollect mirrors FetchAndCollect but for ongoing cron use.
// It does everything the discovery scraper does:
//   - Enriches wrestlers missing profile data
//   - Multi-pass discovery (newly found wrestlers get scraped too)
//   - Scrapes promotion history (page=20) per wrestler
//   - Scrapes unknown promotions
//   - Scrapes title reigns (page=11) per wrestler
//   - Updates promotion assignment per wrestler
//   - Tracks last_scraped_at timestamps
//   - Status tracking via SetStatus
//
// The key difference: uses ProcessNewMatches (inline ELO) instead of CollectMatches.
// This is correct for incremental updates where ELO is already established.
//
// Strategy (hybrid event-first approach):
//   Phase 1: Scrape Cagematch event search for the last 30 days
//   Phase 2: From new events' card pages, find which wrestlers were involved
//   Phase 3: Only scrape THOSE wrestlers' recent match pages (not all 500+)
//   Phase 4: Multi-pass discovery for any newly found wrestlers
//
// This is way more efficient than scraping every wrestler individually.
// The 30-day lookback catches late Cagematch uploads (community-driven, sporadic).
func (s *CagematchScraper) IncrementalFetchAndCollect(proc *Processor) (int, error) {
	// Enrich any wrestlers missing profile data
	s.EnrichWrestlers()

	// Phase 1: Find recent events we might not have
	lookbackDays := 30
	dateCutoff := time.Now().AddDate(0, 0, -lookbackDays)
	log.Printf("[cron] Phase 1: Scanning events from last %d days...", lookbackDays)

	recentEvents, err := s.scrapeRecentEventIDs(lookbackDays)
	if err != nil {
		log.Printf("[cron] Error fetching recent events: %v — falling back to full wrestler scan", err)
		recentEvents = nil
	}

	// Phase 2: Figure out which wrestlers to scrape
	// Start with wrestlers found in new events, then discover more via multi-pass
	wrestlersToScrape := make(map[uint]bool) // cagematch IDs

	if len(recentEvents) > 0 {
		// Filter to events we don't already have complete data for
		var newEvents []recentEvent
		for _, ev := range recentEvents {
			if ev.ID == 0 {
				continue
			}
			var existingMatch models.Match
			if s.db.Where("cagematch_event_id = ?", ev.ID).First(&existingMatch).Error != nil {
				// Event not in DB — it's new
				newEvents = append(newEvents, ev)
			}
		}

		log.Printf("[cron] Found %d total events, %d new (not in DB)", len(recentEvents), len(newEvents))

		// Scrape card pages of new events to find involved wrestlers
		for _, ev := range newEvents {
			s.SetStatus(fmt.Sprintf("[cron] Checking event: %s", ev.Name), true)
			wrestlerIDs, err := s.scrapeEventWrestlerIDs(ev.ID)
			if err != nil {
				log.Printf("[cron] Error fetching card for event %d (%s): %v", ev.ID, ev.Name, err)
				continue
			}
			for _, cmID := range wrestlerIDs {
				wrestlersToScrape[uint(cmID)] = true
			}
			time.Sleep(s.delay)
		}

		log.Printf("[cron] Found %d unique wrestlers from new events", len(wrestlersToScrape))

		// Also add any tracked wrestlers whose cagematch IDs appeared
		// (they might have matches in events we already partially have)
	}

	// If we ended up with no wrestlers to scrape (event scan failed, returned
	// no events, or all events were already in DB), fall back to all tracked wrestlers
	if len(wrestlersToScrape) == 0 {
		if len(recentEvents) == 0 {
			log.Println("[cron] No event data — falling back to all tracked wrestlers")
		} else {
			log.Printf("[cron] All %d events already in DB — no wrestlers to update", len(recentEvents))
			s.SetStatus("", false)
			return 0, nil
		}
		var allWrestlers []models.Wrestler
		s.db.Where("cagematch_id > 0").Find(&allWrestlers)
		for _, w := range allWrestlers {
			wrestlersToScrape[w.CagematchID] = true
		}
	}

	// Phase 3 & 4: Scrape wrestlers with multi-pass discovery
	scraped := make(map[uint]bool)  // cagematch IDs we've already scraped
	knownIDs := make(map[uint]bool) // all wrestler cagematch IDs we knew about before this pass
	totalNew := 0

	// Snapshot current wrestler IDs so we can detect newly discovered ones
	var existingWrestlers []models.Wrestler
	s.db.Select("cagematch_id").Where("cagematch_id > 0").Find(&existingWrestlers)
	for _, w := range existingWrestlers {
		knownIDs[w.CagematchID] = true
	}

	for pass := 1; ; pass++ {
		// Build list of wrestlers to scrape this pass
		var toScrape []models.Wrestler

		if pass == 1 {
			// First pass: scrape the wrestlers we identified from events
			for cmID := range wrestlersToScrape {
				var w models.Wrestler
				if s.db.Where("cagematch_id = ?", cmID).First(&w).Error == nil {
					if !scraped[w.CagematchID] {
						toScrape = append(toScrape, w)
					}
				}
			}
		} else {
			// Subsequent passes: ONLY pick up wrestlers that were newly discovered
			// (created during a previous pass), NOT all unscraped wrestlers
			var wrestlers []models.Wrestler
			s.db.Where("cagematch_id > 0").Find(&wrestlers)
			for _, w := range wrestlers {
				if scraped[w.CagematchID] || w.CagematchID == 0 {
					continue
				}
				// Only scrape if this wrestler is NEW (wasn't in our DB before this run)
				if knownIDs[w.CagematchID] {
					continue
				}
				toScrape = append(toScrape, w)
			}
		}

		if len(toScrape) == 0 {
			log.Printf("[cron] All wrestlers scraped after %d passes", pass-1)
			break
		}

		log.Printf("[cron] === Pass %d: %d wrestlers to scrape ===", pass, len(toScrape))

		for i, w := range toScrape {
			s.SetStatus(fmt.Sprintf("[cron] %s (%d/%d, pass %d)", w.Name, i+1, len(toScrape), pass), true)
			scraped[w.CagematchID] = true

			log.Printf("[cron] Scraping %s (CM#%d) since %s", w.Name, w.CagematchID, dateCutoff.Format("2006-01-02"))

			matches, err := s.scrapeWrestlerMatchesSince(int(w.CagematchID), dateCutoff)
			if err != nil {
				log.Printf("[cron] Error scraping %s: %v", w.Name, err)
				continue
			}

			// Deduplicate and sort chronologically (oldest first) for correct ELO ordering
			matches = deduplicateMatches(matches)
			sort.Slice(matches, func(a, b int) bool {
				return matches[a].Date.Before(matches[b].Date)
			})
			newCount := proc.ProcessNewMatches(matches)
			totalNew += newCount

			// Only scrape promo history + title reigns if we found new matches
			// No point hitting extra pages for wrestlers with nothing new
			if newCount > 0 {
				// Scrape promotion history (page=20)
				promoEntries, promoErr := s.ScrapePromotionHistory(int(w.CagematchID))
				if promoErr != nil {
					log.Printf("[cron] Error scraping promo history for %s: %v", w.Name, promoErr)
				} else if len(promoEntries) > 0 {
					for _, pe := range promoEntries {
						ph := models.PromotionHistory{
							WrestlerID:  w.ID,
							Promotion:   pe.Promotion,
							PromotionID: pe.PromotionID,
							Year:        pe.Year,
							Matches:     pe.Matches,
						}
						s.db.Where("wrestler_id = ? AND promotion = ? AND year = ?", w.ID, pe.Promotion, pe.Year).
							Assign(ph).FirstOrCreate(&ph)
					}
					newPromo := proc.DetermineCurrentPromotion(w.ID)
					if newPromo != "" {
						s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("promotion", newPromo)
					}
					log.Printf("[cron] %s: %d promotion history entries", w.Name, len(promoEntries))

					// Scrape any unknown promotions
					for _, pe := range promoEntries {
						if pe.PromotionID == 0 {
							continue
						}
						var existing models.Promotion
						if s.db.Where("cagematch_id = ?", pe.PromotionID).First(&existing).Error == nil {
							continue
						}
						promoData, err := s.ScrapePromotion(pe.PromotionID)
						if err != nil {
							log.Printf("[cron] Error scraping promotion %d: %v", pe.PromotionID, err)
							continue
						}
						s.db.Create(promoData)
						log.Printf("[cron] Scraped promotion: %s (%s)", promoData.Name, promoData.Country)
						time.Sleep(s.delay)
					}
				}

				// Scrape title reigns (page=11)
				titleEntries, titleErr := s.ScrapeTitleReigns(int(w.CagematchID))
				if titleErr != nil {
					log.Printf("[cron] Error scraping titles for %s: %v", w.Name, titleErr)
				} else if len(titleEntries) > 0 {
					for _, te := range titleEntries {
						tr := models.TitleReign{
							WrestlerID:       w.ID,
							TitleName:        te.TitleName,
							CagematchTitleID: te.CagematchTitleID,
							ReignNumber:      te.ReignNumber,
							WonDate:          te.WonDate,
							LostDate:         te.LostDate,
							DurationDays:     te.DurationDays,
						}
						s.db.Where("wrestler_id = ? AND cagematch_title_id = ? AND won_date = ?", w.ID, te.CagematchTitleID, te.WonDate).
							Assign(tr).FirstOrCreate(&tr)
					}
					log.Printf("[cron] %s: %d title reigns", w.Name, len(titleEntries))
					s.syncTitleReigns(titleEntries, proc)
				}
			} else {
				log.Printf("[cron] %s: no new matches — skipping promo/title scrape", w.Name)
			}
			time.Sleep(s.delay)

			// Mark wrestler as scraped
			now := time.Now()
			s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("last_scraped_at", now)

			log.Printf("[cron] %s: %d matches scraped, %d new stored", w.Name, len(matches), newCount)
			knownIDs[w.CagematchID] = true // mark as known so future passes skip them
			time.Sleep(s.delay)
		}
	}

	s.SetStatus("", false)
	return totalNew, nil
}

// recentEvent holds basic info from the Cagematch event search.
type recentEvent struct {
	ID   int
	Name string
	Date time.Time
}

// scrapeRecentEventIDs searches Cagematch for events in the last N days.
// Returns a list of event IDs and names.
func (s *CagematchScraper) scrapeRecentEventIDs(lookbackDays int) ([]recentEvent, error) {
	now := time.Now()
	from := now.AddDate(0, 0, -lookbackDays)

	var allEvents []recentEvent

	// Cagematch event search paginates with &s=0, &s=100, etc.
	for offset := 0; ; offset += 100 {
		url := fmt.Sprintf(
			"%s/?id=1&view=search&sDateFromDay=%02d&sDateFromMonth=%02d&sDateFromYear=%d&sDateTillDay=%02d&sDateTillMonth=%02d&sDateTillYear=%d&s=%d",
			s.baseURL,
			from.Day(), from.Month(), from.Year(),
			now.Day(), now.Month(), now.Year(),
			offset,
		)

		doc, err := s.fetchPage(url)
		if err != nil {
			return allEvents, err
		}

		eventsBefore := len(allEvents)

		// Each event row has a link like ?id=1&nr=XXXXX
		doc.Find("tr").Each(func(i int, row *goquery.Selection) {
			// Find the event link
			row.Find("a").Each(func(j int, a *goquery.Selection) {
				href, exists := a.Attr("href")
				if !exists {
					return
				}
				// Only event links (id=1), not promotions (id=8)
				eid := extractCagematchID(href, 1)
				if eid > 0 {
					name := strings.TrimSpace(a.Text())
					if name != "" && name != "Card" {
						allEvents = append(allEvents, recentEvent{
							ID:   eid,
							Name: name,
						})
					}
				}
			})
		})

		newOnPage := len(allEvents) - eventsBefore
		if newOnPage == 0 {
			break
		}

		// Check for next page
		nextOffset := fmt.Sprintf("s=%d", offset+100)
		hasNext := false
		doc.Find(".NavigationPartPage a").Each(func(i int, a *goquery.Selection) {
			href, exists := a.Attr("href")
			if exists && strings.Contains(href, nextOffset) {
				hasNext = true
			}
		})
		if !hasNext {
			break
		}

		time.Sleep(s.delay)
	}

	// Deduplicate by event ID
	seen := make(map[int]bool)
	var unique []recentEvent
	for _, ev := range allEvents {
		if !seen[ev.ID] {
			seen[ev.ID] = true
			unique = append(unique, ev)
		}
	}

	log.Printf("[cron] Found %d unique events in last %d days", len(unique), lookbackDays)
	return unique, nil
}

// scrapeEventWrestlerIDs fetches an event's card page and extracts all wrestler
// cagematch IDs mentioned in the card.
func (s *CagematchScraper) scrapeEventWrestlerIDs(eventID int) ([]int, error) {
	url := fmt.Sprintf("%s/?id=1&nr=%d&page=2", s.baseURL, eventID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	seen := make(map[int]bool)
	var ids []int

	doc.Find("a").Each(func(i int, a *goquery.Selection) {
		href, exists := a.Attr("href")
		if !exists {
			return
		}
		cmID := extractCagematchID(href, 2) // id=2 = wrestler
		if cmID > 0 && !seen[cmID] {
			seen[cmID] = true
			ids = append(ids, cmID)
		}
	})

	return ids, nil
}

// RefreshProfiles re-scrapes every tracked wrestler's Cagematch profile
// and updates any fields that have changed (name, promotion, height, weight,
// wrestling style, birthplace, socials, aliases).
// This catches real-world changes like promotion switches, name changes, new socials, etc.
// Returns the number of wrestlers updated.
func (s *CagematchScraper) RefreshProfiles(proc *Processor) (int, error) {
	var wrestlers []models.Wrestler
	s.db.Where("cagematch_id > 0").Find(&wrestlers)

	if len(wrestlers) == 0 {
		return 0, nil
	}

	log.Printf("[profiles] Refreshing profiles for %d wrestlers...", len(wrestlers))

	updated := 0
	for i, w := range wrestlers {
		s.SetStatus(fmt.Sprintf("[profiles] %s (%d/%d)", w.Name, i+1, len(wrestlers)), true)

		profile, err := s.ScrapeWrestlerProfile(int(w.CagematchID))
		if err != nil {
			log.Printf("[profiles] Error scraping %s (CM#%d): %v", w.Name, w.CagematchID, err)
			time.Sleep(s.delay)
			continue
		}
		if profile == nil {
			// Shouldn't happen for tracked wrestlers but just in case
			time.Sleep(s.delay)
			continue
		}

		// Build updates map — only update fields that changed
		changes := make(map[string]interface{})

		if profile.Name != "" && profile.Name != w.Name {
			changes["name"] = profile.Name
			log.Printf("[profiles] %s → name changed: %s → %s", w.Name, w.Name, profile.Name)
		}
		if !profile.Birthday.IsZero() && profile.Birthday != w.Birthday {
			changes["birthday"] = profile.Birthday
		}
		if profile.Birthplace != "" && profile.Birthplace != w.Birthplace {
			changes["birthplace"] = profile.Birthplace
		}
		if profile.Height != "" && profile.Height != w.Height {
			changes["height"] = profile.Height
		}
		if profile.Weight != "" && profile.Weight != w.Weight {
			changes["weight"] = profile.Weight
		}
		if profile.DebutYear > 0 && profile.DebutYear != w.DebutYear {
			changes["debut_year"] = profile.DebutYear
		}
		if profile.WrestlingStyle != "" && profile.WrestlingStyle != w.WrestlingStyle {
			changes["wrestling_style"] = profile.WrestlingStyle
		}

		// Promotion from profile — only update if the profile has a concrete one
		// (tiered promotion logic from DetermineCurrentPromotion is still the authority,
		// but the profile field feeds into tier 1 of that logic)
		if profile.Promotion != "" && profile.Promotion != w.Promotion {
			// Re-run tiered promotion assignment (profile data feeds tier 1)
			// First update the raw profile promotion so tier 1 picks it up
			changes["promotion"] = profile.Promotion
			log.Printf("[profiles] %s → promotion changed: %s → %s", w.Name, w.Promotion, profile.Promotion)
		}

		if len(changes) > 0 {
			s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Updates(changes)
			updated++
			log.Printf("[profiles] Updated %s: %d fields changed", w.Name, len(changes))
		}

		// Sync aliases — add any new ones we don't have
		if len(profile.AlterEgos) > 0 {
			var existingAliases []models.WrestlerAlias
			s.db.Where("wrestler_id = ?", w.ID).Find(&existingAliases)
			existingSet := make(map[string]bool)
			for _, a := range existingAliases {
				existingSet[a.Alias] = true
			}
			for _, alias := range profile.AlterEgos {
				if !existingSet[alias] {
					s.db.Create(&models.WrestlerAlias{WrestlerID: w.ID, Alias: alias})
					log.Printf("[profiles] %s → new alias: %s", w.Name, alias)
				}
			}
		}

		// Sync socials — add new ones, update URLs for existing
		if len(profile.Socials) > 0 {
			var existingSocials []models.Socials
			s.db.Where("wrestler_id = ?", w.ID).Find(&existingSocials)
			existingMap := make(map[string]models.Socials)
			for _, soc := range existingSocials {
				existingMap[soc.Name] = soc
			}
			for name, url := range profile.Socials {
				if existing, ok := existingMap[name]; ok {
					// Update URL if changed
					if existing.URL != url {
						s.db.Model(&models.Socials{}).Where("id = ?", existing.ID).Update("url", url)
						log.Printf("[profiles] %s → updated %s URL", w.Name, name)
					}
				} else {
					// New social
					s.db.Create(&models.Socials{WrestlerID: w.ID, Name: name, URL: url})
					log.Printf("[profiles] %s → new social: %s", w.Name, name)
				}
			}
		}

		time.Sleep(s.delay)
	}

	s.SetStatus("", false)
	log.Printf("[profiles] Refresh complete — %d/%d wrestlers had updates", updated, len(wrestlers))
	return updated, nil
}

// ValidateAndRescrape compares promotion_histories (expected match counts from CM page=20)
// against actual match counts in the DB for each wrestler. Wrestlers with mismatches
// are re-scraped to fill in missing matches.
// Returns: total number of new matches added, number of wrestlers re-scraped.
func (s *CagematchScraper) ValidateAndRescrape(proc *Processor) (int, int, error) {
	log.Println("[validator] Starting match count validation...")

	// Get all wrestlers with promotion history data
	var wrestlers []models.Wrestler
	s.db.Where("cagematch_id > 0").Find(&wrestlers)

	// Build a map of wrestler ID -> wrestler for quick lookup
	wrestlerMap := make(map[uint]models.Wrestler)
	for _, w := range wrestlers {
		wrestlerMap[w.ID] = w
	}

	// For each wrestler, compare expected vs actual match counts per promotion/year
	type mismatch struct {
		Wrestler      models.Wrestler
		ExpectedTotal int
		ActualTotal   int
		Missing       int
	}
	var mismatches []mismatch

	for _, w := range wrestlers {
		// Get expected counts from promotion_histories (scraped from CM page=20)
		var promoHistories []models.PromotionHistory
		s.db.Where("wrestler_id = ?", w.ID).Find(&promoHistories)
		if len(promoHistories) == 0 {
			continue
		}

		expectedTotal := 0
		for _, ph := range promoHistories {
			expectedTotal += ph.Matches
		}

		// Get actual match count from DB
		var actualTotal int64
		s.db.Model(&models.MatchParticipant{}).Where("wrestler_id = ?", w.ID).Count(&actualTotal)

		if int(actualTotal) < expectedTotal {
			missing := expectedTotal - int(actualTotal)
			mismatches = append(mismatches, mismatch{
				Wrestler:      w,
				ExpectedTotal: expectedTotal,
				ActualTotal:   int(actualTotal),
				Missing:       missing,
			})
		}
	}

	if len(mismatches) == 0 {
		log.Println("[validator] All wrestlers validated — no mismatches found!")
		return 0, 0, nil
	}

	// Sort by most missing first
	sort.Slice(mismatches, func(i, j int) bool {
		return mismatches[i].Missing > mismatches[j].Missing
	})

	log.Printf("[validator] Found %d wrestlers with missing matches:", len(mismatches))
	for i, mm := range mismatches {
		log.Printf("[validator]   %d. %s: expected %d, have %d, missing %d",
			i+1, mm.Wrestler.Name, mm.ExpectedTotal, mm.ActualTotal, mm.Missing)
	}

	// Re-scrape each wrestler with missing matches
	totalNew := 0
	rescrapeCount := 0

	for i, mm := range mismatches {
		w := mm.Wrestler
		s.mu.Lock()
		s.CurrentWrestler = fmt.Sprintf("[validator] %s (%d/%d, missing %d)", w.Name, i+1, len(mismatches), mm.Missing)
		s.IsRunning = true
		s.mu.Unlock()

		log.Printf("[validator] Re-scraping %s (CM#%d) — missing %d matches", w.Name, w.CagematchID, mm.Missing)

		matches, err := s.scrapeWrestlerMatches(int(w.CagematchID))
		if err != nil {
			log.Printf("[validator] Error re-scraping %s: %v", w.Name, err)
			continue
		}

		matches = deduplicateMatches(matches)
		newCount := proc.CollectMatches(matches)
		totalNew += newCount
		rescrapeCount++

		// Update last_scraped_at
		now := time.Now()
		s.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("last_scraped_at", now)

		log.Printf("[validator] %s: re-scraped %d matches, %d new stored", w.Name, len(matches), newCount)
		time.Sleep(s.delay)
	}

	s.mu.Lock()
	s.CurrentWrestler = ""
	s.IsRunning = false
	s.mu.Unlock()

	log.Printf("[validator] Complete — re-scraped %d wrestlers, added %d new matches", rescrapeCount, totalNew)
	return totalNew, rescrapeCount, nil
}

// IsSkipped checks if a CagematchID is in the skip list (thread-safe).
func (s *CagematchScraper) IsSkipped(cagematchID int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipList[cagematchID]
}

// GetStatus returns the current scraper state (thread-safe).
func (s *CagematchScraper) GetStatus() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CurrentWrestler, s.IsRunning
}

// --- HTTP Helpers ---

// fetchPage makes an HTTP GET request and returns a goquery document.
// goquery is Go's BeautifulSoup — it lets you use CSS selectors on HTML.
//
// Python equivalent:
//   resp = requests.get(url, headers={"User-Agent": "..."})
//   soup = BeautifulSoup(resp.text, "html.parser")
func (s *CagematchScraper) fetchPage(url string) (*goquery.Document, error) {
	// Create a custom request so we can set headers
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Be honest about who we are
	req.Header.Set("User-Agent", "JoshiRankingsBot/1.0 (wrestling ELO project)")

	// Make the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close() // ALWAYS close the body in Go — like Python's `with` statement

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("got status %d for %s", resp.StatusCode, url)
	}

	// Parse HTML into a queryable document
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	return doc, nil
}

// --- Wrestler Profile Scraping ---

// WrestlerProfile holds all the data we can grab from a Cagematch profile page.
// URL pattern: https://www.cagematch.net/?id=2&nr={CAGEMATCH_ID}
type WrestlerProfile struct {
	CagematchID    int
	Name           string
	Gender         string
	Birthday       time.Time
	Birthplace     string
	Height         string
	Weight         string
	Promotion      string
	WrestlingStyle string
	DebutYear      int
	AlterEgos      []string // maps to our WrestlerAlias table
	SignatureMoves []string
	Socials        map[string]string // name -> URL
}

// ScrapeWrestlerProfile fetches a wrestler's full profile from Cagematch.
// This is called when we discover a new wrestler in match results.
//
// Returns nil if the wrestler is male (we only track women).
func (s *CagematchScraper) ScrapeWrestlerProfile(cagematchID int) (*WrestlerProfile, error) {
	// Check skip list first — don't re-fetch known males
	s.mu.Lock()
	if s.skipList[cagematchID] {
		s.mu.Unlock()
		return nil, nil // nil profile = skip this wrestler
	}
	s.mu.Unlock()

	url := fmt.Sprintf("%s/?id=2&nr=%d", s.baseURL, cagematchID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	profile := &WrestlerProfile{
		CagematchID: cagematchID,
		Socials:     make(map[string]string),
	}

	// Get name from page header as fallback (always present)
	// <h1 class="TextHeader">Lita</h1>
	profile.Name = strings.TrimSpace(doc.Find("h1.TextHeader").First().Text())

	// Parse the info box — each row has a title and contents div
	// HTML structure:
	//   <div class="InformationBoxRow">
	//     <div class="InformationBoxTitle">Gender:</div>
	//     <div class="InformationBoxContents">female</div>
	//   </div>
	//
	// goquery's .Each() is like Python's:
	//   for row in soup.select(".InformationBoxRow"):
	doc.Find(".InformationBoxRow").Each(func(i int, row *goquery.Selection) {
		title := strings.TrimSpace(row.Find(".InformationBoxTitle").Text())
		contents := strings.TrimSpace(row.Find(".InformationBoxContents").Text())

		// Switch is Go's match/case — cleaner than if/elif chains
		switch title {
		case "Current gimmick:":
			profile.Name = contents
		case "Gender:":
			profile.Gender = strings.ToLower(contents)
		case "Birthday:":
			profile.Birthday = parseCagematchDate(contents)
		case "Birthplace:":
			profile.Birthplace = contents
		case "Height:":
			profile.Height = contents
		case "Weight:":
			profile.Weight = contents
		case "Promotion:":
			profile.Promotion = contents
		case "Wrestling style:":
			profile.WrestlingStyle = contents
		case "Beginning of in-ring career:":
			if year, err := strconv.Atoi(contents[:4]); err == nil {
				profile.DebutYear = year
			}
		case "Alter egos:":
			// Alter egos are comma-separated links
			row.Find(".InformationBoxContents a").Each(func(j int, a *goquery.Selection) {
				alias := strings.TrimSpace(a.Text())
				if alias != "" {
					profile.AlterEgos = append(profile.AlterEgos, alias)
				}
			})
		case "WWW:":
			// Social links are <a> tags inside the contents
			row.Find(".InformationBoxContents a").Each(func(j int, a *goquery.Selection) {
				href, exists := a.Attr("href")
				if exists {
					name := guessSocialName(href)
					profile.Socials[name] = href
				}
			})
		}
	})

	// Parse signature moves from the moves section (if it exists)
	doc.Find(".MatchResults").Each(func(i int, sel *goquery.Selection) {
		// Signature moves are listed in a specific section — we'll grab them
		// if the page has them. Not all profiles do.
	})

	// GENDER FILTER — the key check!
	if profile.Gender != "female" && profile.Gender != "diverse" {
		log.Printf("[cagematch] Skipping %s (gender: %s)", profile.Name, profile.Gender)
		s.mu.Lock()
		s.skipList[cagematchID] = true
		s.mu.Unlock()
		// Persist to DB so we don't re-check after restart
		s.db.FirstOrCreate(&models.SkippedWrestler{CagematchID: cagematchID, Name: profile.Name}, models.SkippedWrestler{CagematchID: cagematchID})
		return nil, nil // nil = not female, skip
	}

	log.Printf("[cagematch] Discovered: %s (%s, %s)", profile.Name, profile.Promotion, profile.Gender)
	return profile, nil
}

// ScrapePromotionHistory fetches a wrestler's "Matches per Promotion and Year" from page=20.
// Returns a slice of (promotion_name, promotion_id, year, match_count) entries.
type PromotionHistoryEntry struct {
	Promotion   string
	PromotionID int
	Year        int
	Matches     int
}

func (s *CagematchScraper) ScrapePromotionHistory(cagematchID int) ([]PromotionHistoryEntry, error) {
	url := fmt.Sprintf("https://www.cagematch.net/?id=2&nr=%d&page=20", cagematchID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	var entries []PromotionHistoryEntry

	// Each row in the first table has: year | promotion logos with match counts
	doc.Find(".TBase tr").Each(func(i int, row *goquery.Selection) {
		// Skip header rows
		if row.HasClass("THeaderRow") {
			return
		}

		// First cell is the year
		yearText := strings.TrimSpace(row.Find("td").First().Text())
		year, err := strconv.Atoi(yearText)
		if err != nil {
			return
		}

		// Each promotion is an <a> containing an <img> with alt="PromotionName (X Matches)"
		row.Find("a").Each(func(j int, a *goquery.Selection) {
			img := a.Find("img")
			if img.Length() == 0 {
				return
			}

			title, exists := img.Attr("title")
			if !exists {
				title, exists = img.Attr("alt")
			}
			if !exists {
				return
			}

			// Parse "World Wonder Ring Stardom (114 Matches)"
			// Find the last occurrence of " (X Matches)"
			idx := strings.LastIndex(title, " (")
			if idx < 0 {
				return
			}
			promoName := title[:idx]
			countStr := title[idx+2:]
			countStr = strings.TrimSuffix(countStr, ")")
			countStr = strings.TrimSuffix(countStr, " Matches")
			countStr = strings.TrimSuffix(countStr, " Match")
			matchCount, err := strconv.Atoi(strings.TrimSpace(countStr))
			if err != nil {
				return
			}

			// Extract promotion ID from the href
			promoID := 0
			href, exists := a.Attr("href")
			if exists {
				// href like "?id=2&nr=10402&page=4&year=2024&promotion=745"
				if pidIdx := strings.Index(href, "promotion="); pidIdx >= 0 {
					pidStr := href[pidIdx+10:]
					if ampIdx := strings.Index(pidStr, "&"); ampIdx >= 0 {
						pidStr = pidStr[:ampIdx]
					}
					promoID, _ = strconv.Atoi(pidStr)
				}
			}

			entries = append(entries, PromotionHistoryEntry{
				Promotion:   promoName,
				PromotionID: promoID,
				Year:        year,
				Matches:     matchCount,
			})
		})
	})

	return entries, nil
}

// ScrapeTitleReigns fetches a wrestler's title history from page=11.
type TitleReignEntry struct {
	TitleName      string
	CagematchTitleID int
	ReignNumber    int
	WonDate        time.Time
	LostDate       *time.Time
	DurationDays   int
}

func (s *CagematchScraper) ScrapeTitleReigns(cagematchID int) ([]TitleReignEntry, error) {
	url := fmt.Sprintf("https://www.cagematch.net/?id=2&nr=%d&page=11", cagematchID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	var entries []TitleReignEntry

	doc.Find(".TBase tr").Each(func(i int, row *goquery.Selection) {
		if row.HasClass("THeaderRow") {
			return
		}

		cols := row.Find("td")
		if cols.Length() < 4 {
			return
		}

		// Column 0: Timeframe (e.g. "03.01.2026 - " or "23.04.2023 - 27.04.2025")
		timeframe := strings.TrimSpace(cols.Eq(0).Text())

		// Column 1: Title name (contains <a> with title link)
		titleLink := cols.Eq(1).Find("a").First()
		titleName := strings.TrimSpace(titleLink.Text())
		if titleName == "" || titleName == "Matches" {
			return
		}

		// Extract title ID from href like "?id=5&nr=1577"
		titleID := 0
		if href, exists := titleLink.Attr("href"); exists {
			if nrIdx := strings.Index(href, "nr="); nrIdx >= 0 {
				nrStr := href[nrIdx+3:]
				if ampIdx := strings.Index(nrStr, "&"); ampIdx >= 0 {
					nrStr = nrStr[:ampIdx]
				}
				titleID, _ = strconv.Atoi(nrStr)
			}
		}

		// Column 2: Duration (e.g. "735 days")
		durationText := strings.TrimSpace(cols.Eq(2).Text())
		durationText = strings.ReplaceAll(durationText, "\u00a0", " ")
		durationDays := 0
		if dIdx := strings.Index(durationText, " day"); dIdx >= 0 {
			durationDays, _ = strconv.Atoi(strings.TrimSpace(durationText[:dIdx]))
		}

		// Parse dates from timeframe
		// Format: "DD.MM.YYYY - DD.MM.YYYY" or "DD.MM.YYYY - " (still holding)
		parts := strings.SplitN(timeframe, "-", 2)
		if len(parts) < 1 {
			return
		}

		wonDate, err := time.Parse("02.01.2006", strings.TrimSpace(parts[0]))
		if err != nil {
			return
		}

		var lostDate *time.Time
		if len(parts) > 1 {
			lostStr := strings.TrimSpace(parts[1])
			if lostStr != "" {
				if parsed, err := time.Parse("02.01.2006", lostStr); err == nil {
					lostDate = &parsed
				}
			}
		}

		entries = append(entries, TitleReignEntry{
			TitleName:        titleName,
			CagematchTitleID: titleID,
			WonDate:          wonDate,
			LostDate:         lostDate,
			DurationDays:     durationDays,
		})
	})

	// Number the reigns per title (reverse order since CM lists newest first)
	titleCounts := make(map[string]int)
	// Count total reigns per title first
	for _, e := range entries {
		titleCounts[e.TitleName]++
	}
	reignCounters := make(map[string]int)
	for i := len(entries) - 1; i >= 0; i-- {
		reignCounters[entries[i].TitleName]++
		entries[i].ReignNumber = reignCounters[entries[i].TitleName]
	}

	return entries, nil
}

// syncTitleReigns scrapes the title history page for any title found in a wrestler's
// title reigns. This keeps our data in sync with Cagematch (catches tag partners,
// corrects #1 contender matches mismarked as title matches, etc.).
// Only scrapes titles we just found reigns for — not all 1696.
func (s *CagematchScraper) syncTitleReigns(entries []TitleReignEntry, proc *Processor) {
	// Collect unique title IDs
	titles := make(map[int]string)
	for _, te := range entries {
		if te.CagematchTitleID > 0 {
			titles[te.CagematchTitleID] = te.TitleName
		}
	}

	for titleID, titleName := range titles {
		log.Printf("[titles-sync] Syncing %s (#%d)", titleName, titleID)
		history, err := s.ScrapeTitleHistory(titleID)
		time.Sleep(s.delay)
		if err != nil {
			log.Printf("[titles-sync] Error scraping title history for %s: %v", titleName, err)
			continue
		}

		// Sync each reign from the title history
		for _, entry := range history {
			for _, holder := range entry.Holders {
				if holder.CMID == 0 && holder.Name == "" {
					continue
				}

				var wrestlerID uint
				var untracked bool
				var holderName string

				if holder.CMID > 0 {
					var wrestler models.Wrestler
					if err := s.db.Where("cagematch_id = ?", holder.CMID).First(&wrestler).Error; err == nil {
						wrestlerID = wrestler.ID
						holderName = wrestler.Name
					} else {
						untracked = true
						holderName = holder.Name
					}
				} else {
					untracked = true
					holderName = holder.Name
				}

				tr := models.TitleReign{
					WrestlerID:        wrestlerID,
					TitleName:         titleName,
					CagematchTitleID:  titleID,
					ReignNumber:       entry.ReignNumber,
					WonDate:           entry.WonDate,
					LostDate:          entry.LostDate,
					DurationDays:      entry.DurationDays,
					Untracked:         untracked,
					HolderName:        holderName,
					HolderCagematchID: holder.CMID,
				}

				// Dedup by title + won_date + holder
				if wrestlerID > 0 {
					s.db.Where("cagematch_title_id = ? AND won_date = ? AND wrestler_id = ?",
						titleID, entry.WonDate, wrestlerID).
						Assign(tr).FirstOrCreate(&tr)
				} else if holder.CMID > 0 {
					s.db.Where("cagematch_title_id = ? AND won_date = ? AND holder_cagematch_id = ?",
						titleID, entry.WonDate, holder.CMID).
						Assign(tr).FirstOrCreate(&tr)
				} else {
					s.db.Where("cagematch_title_id = ? AND won_date = ? AND holder_name = ?",
						titleID, entry.WonDate, holderName).
						Assign(tr).FirstOrCreate(&tr)
				}
			}
		}
		log.Printf("[titles-sync] Synced %s: %d reigns from history", titleName, len(history))
	}
}

// --- Title History Scraping (per-title) ---

// TitleHolder represents one person in a reign (tag titles have multiple).
type TitleHolder struct {
	Name   string
	CMID   int
}

// TitleHistoryEntry represents one reign from a title's history page.
// Tag titles will have multiple holders.
type TitleHistoryEntry struct {
	ReignNumber    int
	Holders        []TitleHolder
	WonDate        time.Time
	LostDate       *time.Time
	DurationDays   int
}

// ScrapeTitleInfo fetches the title's main page (?id=5&nr=X) for metadata.
func (s *CagematchScraper) ScrapeTitleInfo(titleID int) (*models.Title, error) {
	url := fmt.Sprintf("%s/?id=5&nr=%d", s.baseURL, titleID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	title := &models.Title{
		CagematchID: titleID,
	}

	doc.Find(".InformationBoxRow").Each(func(i int, row *goquery.Selection) {
		label := strings.TrimSpace(row.Find(".InformationBoxTitle").Text())
		content := strings.TrimSpace(row.Find(".InformationBoxContents").Text())

		switch label {
		case "Current name:":
			title.Name = content
		case "Promotion:":
			title.Promotion = content
			// Extract promotion ID from link
			if href, exists := row.Find(".InformationBoxContents a").Attr("href"); exists {
				if nrIdx := strings.Index(href, "nr="); nrIdx >= 0 {
					nrStr := href[nrIdx+3:]
					if ampIdx := strings.Index(nrStr, "&"); ampIdx >= 0 {
						nrStr = nrStr[:ampIdx]
					}
					title.PromotionID, _ = strconv.Atoi(nrStr)
				}
			}
		case "Status:":
			title.Status = content
		}
	})

	return title, nil
}

// ScrapeTitleHistory fetches the full title history from the main title page (?id=5&nr=X).
// The reign table uses single-cell rows with ChampionDetailsText divs containing:
//   #REIGN_NUM
//   HOLDER_NAME(S) with wrestler links (id=2)
//   DD.MM.YYYY - DD.MM.YYYY (DURATION days)
func (s *CagematchScraper) ScrapeTitleHistory(titleID int) ([]TitleHistoryEntry, error) {
	url := fmt.Sprintf("%s/?id=5&nr=%d", s.baseURL, titleID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	var entries []TitleHistoryEntry
	dateRegex := regexp.MustCompile(`(\d{2}\.\d{2}\.\d{4})`)
	reignRegex := regexp.MustCompile(`#(\d+)`)

	doc.Find(".ChampionDetailsText").Each(func(i int, div *goquery.Selection) {
		text := div.Text()

		// Parse reign number from "#NN"
		reignNum := 0
		if m := reignRegex.FindStringSubmatch(text); len(m) >= 2 {
			reignNum, _ = strconv.Atoi(m[1])
		}

		// Parse wrestler holders from links with id=2
		var holders []TitleHolder
		div.Find("a").Each(func(j int, a *goquery.Selection) {
			href, exists := a.Attr("href")
			if !exists {
				return
			}
			// Only match wrestler links (id=2), not tag teams (id=28) or stables (id=29)
			if !strings.Contains(href, "id=2&") {
				return
			}
			name := strings.TrimSpace(a.Text())
			cmID := 0
			if nrIdx := strings.Index(href, "nr="); nrIdx >= 0 {
				nrStr := href[nrIdx+3:]
				if ampIdx := strings.Index(nrStr, "&"); ampIdx >= 0 {
					nrStr = nrStr[:ampIdx]
				}
				cmID, _ = strconv.Atoi(nrStr)
			}
			if name != "" && name != "Matches" {
				holders = append(holders, TitleHolder{Name: name, CMID: cmID})
			}
		})

		// Check for Vacant in bold text or full text
		boldText := strings.TrimSpace(div.Find(".TextBold").Text())
		if strings.Contains(boldText, "Vacant") || strings.Contains(boldText, "vacant") ||
			strings.Contains(boldText, "VACANT") ||
			strings.Contains(text, "VACANT") || strings.Contains(text, "Vacant") {
			return
		}

		if len(holders) == 0 {
			// Try to get name from bold text if no wrestler links
			if boldText != "" && !strings.Contains(boldText, "day") {
				holders = append(holders, TitleHolder{Name: boldText})
			} else {
				return
			}
		}

		// Parse dates
		dates := dateRegex.FindAllString(text, -1)
		var wonDate time.Time
		var lostDate *time.Time

		if len(dates) >= 1 {
			if parsed, err := time.Parse("02.01.2006", dates[0]); err == nil {
				wonDate = parsed
			}
		}
		if len(dates) >= 2 {
			if parsed, err := time.Parse("02.01.2006", dates[1]); err == nil {
				lostDate = &parsed
			}
		}

		if wonDate.IsZero() {
			return
		}

		// Duration
		durationDays := 0
		if lostDate != nil {
			durationDays = int(lostDate.Sub(wonDate).Hours() / 24)
		} else {
			durationDays = int(time.Since(wonDate).Hours() / 24)
		}

		entries = append(entries, TitleHistoryEntry{
			ReignNumber:  reignNum,
			Holders:      holders,
			WonDate:      wonDate,
			LostDate:     lostDate,
			DurationDays: durationDays,
		})
	})

	return entries, nil
}

// CollectTitleHistories scrapes title histories per-title instead of per-wrestler.
// 1. Discover title IDs from existing title_reigns + wrestler title pages
// 2. For each title, scrape full history
// 3. Skip titles with zero female holders
// 4. Mark male/unknown holders as "untracked"
func (s *CagematchScraper) CollectTitleHistories(proc *Processor) (int, error) {
	log.Println("[titles] Collecting title histories (per-title mode)...")

	// Step 1: Discover title IDs from existing data
	titleIDs := make(map[int]bool)
	var existing []models.TitleReign
	s.db.Select("DISTINCT cagematch_title_id").Where("cagematch_title_id > 0").Find(&existing)
	for _, tr := range existing {
		titleIDs[tr.CagematchTitleID] = true
	}

	// Also check the titles table
	var existingTitles []models.Title
	s.db.Where("cagematch_id > 0").Find(&existingTitles)
	for _, t := range existingTitles {
		titleIDs[t.CagematchID] = true
	}

	log.Printf("[titles] Found %d known title IDs", len(titleIDs))

	totalReigns := 0
	titlesProcessed := 0
	titlesSkipped := 0

	for titleID := range titleIDs {
		s.SetStatus(fmt.Sprintf("Title #%d (%d processed)", titleID, titlesProcessed), true)

		// Scrape title info
		titleInfo, err := s.ScrapeTitleInfo(titleID)
		if err != nil {
			log.Printf("[titles] Error fetching title info for #%d: %v", titleID, err)
			time.Sleep(s.delay)
			continue
		}
		time.Sleep(s.delay)

		// Scrape full history
		entries, err := s.ScrapeTitleHistory(titleID)
		if err != nil {
			log.Printf("[titles] Error fetching history for %s (#%d): %v", titleInfo.Name, titleID, err)
			time.Sleep(s.delay)
			continue
		}
		time.Sleep(s.delay)

		// Check if any holder is female (in our DB or discoverable)
		hasFemale := false
		type resolvedHolder struct {
			entry      TitleHistoryEntry
			holderName string
			holderCMID int
			wrestlerID uint
			untracked  bool
		}
		var resolved []resolvedHolder

		for _, entry := range entries {
			for _, holder := range entry.Holders {
				if holder.CMID == 0 {
					resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: 0, untracked: true})
					continue
				}

				// Check if female wrestler in our DB
				var wrestler models.Wrestler
				if err := s.db.Where("cagematch_id = ?", holder.CMID).First(&wrestler).Error; err == nil {
					hasFemale = true
					// NOTE: Do NOT update wrestler name from title history — the holder name
					// is the gimmick they held the title under, not necessarily their current name.
					// Name updates should only come from ScrapeWrestlerProfile (current gimmick).
					resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: holder.CMID, wrestlerID: wrestler.ID, untracked: false})
					continue
				}

				// Check skip list (known male)
				if s.IsSkipped(holder.CMID) {
					resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: holder.CMID, untracked: true})
					continue
				}

				// Unknown — scrape profile to check gender
				profile, err := s.ScrapeWrestlerProfile(holder.CMID)
				time.Sleep(s.delay)

				if err != nil {
					log.Printf("[titles] Error checking profile for %s (CM#%d): %v", holder.Name, holder.CMID, err)
					resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: holder.CMID, untracked: true})
					continue
				}

				if profile == nil {
					// Male — already added to skip list by ScrapeWrestlerProfile
					resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: holder.CMID, untracked: true})
					continue
				}

				// Female! Create wrestler in DB
				hasFemale = true
				newWrestler := models.Wrestler{
					CagematchID:    uint(profile.CagematchID),
					Name:           profile.Name,
					Birthday:       profile.Birthday,
					Birthplace:     profile.Birthplace,
					Height:         profile.Height,
					Weight:         profile.Weight,
					DebutYear:      profile.DebutYear,
					WrestlingStyle: profile.WrestlingStyle,
					Promotion:      profile.Promotion,
					ELO:            1200,
				}
				if err := s.db.Create(&newWrestler).Error; err != nil {
					log.Printf("[titles] Error creating wrestler %s: %v", profile.Name, err)
					resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: holder.CMID, untracked: true})
					continue
				}
				log.Printf("[titles] Discovered wrestler: %s", newWrestler.Name)
				resolved = append(resolved, resolvedHolder{entry: entry, holderName: holder.Name, holderCMID: holder.CMID, wrestlerID: newWrestler.ID, untracked: false})
			}
		}

		if !hasFemale {
			log.Printf("[titles] Skipping %s (#%d) — no female holders", titleInfo.Name, titleID)
			titlesSkipped++
			continue
		}

		// Store title metadata
		titleInfo.HasFemaleReign = true
		s.db.Where("cagematch_id = ?", titleID).Assign(titleInfo).FirstOrCreate(titleInfo)

		// Delete old per-wrestler entries for this title that will be replaced by per-title data.
		// Per-wrestler entries have holder_name='' and reign_number=1 (wrong numbering).
		// Keep them only if no per-title entry exists for same title+wrestler+won_date.
		s.db.Exec(`
			DELETE FROM title_reigns WHERE id IN (
				SELECT tr1.id FROM title_reigns tr1
				WHERE tr1.cagematch_title_id = ? 
				AND (tr1.holder_name IS NULL OR tr1.holder_name = '')
				AND tr1.wrestler_id > 0
			)
		`, titleID)

		// Upsert all reigns (one row per holder — tag titles get multiple rows with same reign number)
		for _, r := range resolved {
			reign := models.TitleReign{
				WrestlerID:        r.wrestlerID,
				TitleName:         titleInfo.Name,
				CagematchTitleID:  titleID,
				ReignNumber:       r.entry.ReignNumber,
				WonDate:           r.entry.WonDate,
				LostDate:          r.entry.LostDate,
				DurationDays:      r.entry.DurationDays,
				Untracked:         r.untracked,
				HolderName:        r.holderName,
				HolderCagematchID: r.holderCMID,
			}

			// Dedup by title + won_date + holder (use wrestler_id if available, else holder_cagematch_id)
			if r.wrestlerID > 0 {
				s.db.Where("cagematch_title_id = ? AND won_date = ? AND wrestler_id = ?",
					titleID, r.entry.WonDate, r.wrestlerID).
					Assign(reign).FirstOrCreate(&reign)
			} else if r.holderCMID > 0 {
				s.db.Where("cagematch_title_id = ? AND won_date = ? AND holder_cagematch_id = ?",
					titleID, r.entry.WonDate, r.holderCMID).
					Assign(reign).FirstOrCreate(&reign)
			} else {
				// No CM ID — dedup by name + date
				s.db.Where("cagematch_title_id = ? AND won_date = ? AND holder_name = ?",
					titleID, r.entry.WonDate, r.holderName).
					Assign(reign).FirstOrCreate(&reign)
			}
			totalReigns++
		}

		// Close stale open reigns: if Cagematch says a reign ended (has lost_date),
		// but our DB still has it open, close it. Also close any open reign that isn't
		// in the latest entry (i.e. title changed hands).
		if len(entries) > 0 {
			latestWonDate := entries[0].WonDate // entries are newest-first from Cagematch
			// Close any open reigns that are older than the current holder's won date
			s.db.Exec(`
				UPDATE title_reigns 
				SET lost_date = ?, duration_days = CAST((julianday(?) - julianday(won_date)) AS INTEGER)
				WHERE cagematch_title_id = ? AND lost_date IS NULL AND won_date < ?
			`, latestWonDate, latestWonDate, titleID, latestWonDate)
		}

		titlesProcessed++
		log.Printf("[titles] %s: %d reigns stored", titleInfo.Name, len(resolved))
	}

	s.SetStatus("", false)
	log.Printf("[titles] Complete — %d titles processed, %d skipped, %d total reigns", titlesProcessed, titlesSkipped, totalReigns)
	return totalReigns, nil
}

// ScrapePromotion fetches promotion info from Cagematch (id=8&nr=X).
// Returns name, abbreviation, location, country, status.
func (s *CagematchScraper) ScrapePromotion(cagematchPromoID int) (*models.Promotion, error) {
	url := fmt.Sprintf("https://www.cagematch.net/?id=8&nr=%d", cagematchPromoID)
	doc, err := s.fetchPage(url)
	if err != nil {
		return nil, err
	}

	promo := &models.Promotion{
		CagematchID: cagematchPromoID,
	}

	doc.Find(".InformationBoxRow").Each(func(i int, row *goquery.Selection) {
		title := strings.TrimSpace(row.Find(".InformationBoxTitle").Text())
		content := strings.TrimSpace(row.Find(".InformationBoxContents").Text())

		switch title {
		case "Current name:":
			promo.Name = content
		case "Current abbreviation:":
			promo.Abbreviation = content
		case "Location:":
			promo.Location = content
			// Extract country (last part after comma)
			parts := strings.Split(content, ",")
			if len(parts) > 0 {
				promo.Country = strings.TrimSpace(parts[len(parts)-1])
			}
			promo.Region = countryToRegion(promo.Country)
		case "Status:":
			promo.Status = content
		case "Active Time:":
			parts := strings.SplitN(content, " - ", 2)
			if len(parts) >= 1 {
				promo.ActiveFrom = strings.TrimSpace(parts[0])
			}
			if len(parts) >= 2 {
				promo.ActiveTo = strings.TrimSpace(parts[1])
			}
		}
	})

	return promo, nil
}

// countryToRegion maps a country name to a broad region.
func countryToRegion(country string) string {
	country = strings.ToLower(strings.TrimSpace(country))
	switch {
	case country == "japan":
		return "Japan"
	case country == "usa" || country == "canada":
		return "North America"
	case country == "mexico" || country == "puerto rico":
		return "Latin America"
	case country == "england" || country == "scotland" || country == "wales" ||
		country == "ireland" || country == "united kingdom" || country == "great britain":
		return "UK"
	case country == "germany" || country == "france" || country == "spain" ||
		country == "italy" || country == "austria" || country == "finland" ||
		country == "sweden" || country == "switzerland" || country == "belgium" ||
		country == "netherlands" || country == "poland" || country == "czech republic" ||
		country == "hungary" || country == "norway" || country == "denmark" ||
		country == "portugal" || country == "russia" || country == "turkey":
		return "Europe"
	case country == "china" || country == "south korea" || country == "taiwan" ||
		country == "singapore" || country == "hong kong" || country == "thailand" ||
		country == "india" || country == "philippines":
		return "Asia"
	case country == "australia" || country == "new zealand":
		return "Oceania"
	default:
		if country != "" {
			return "Other"
		}
		return ""
	}
}

// --- Match History Scraping ---

// scrapeWrestlerMatches fetches all matches from a wrestler's history page.
// URL pattern: https://www.cagematch.net/?id=2&nr={ID}&page=4
//
// The HTML structure is a TABLE with rows like:
//   <tr>
//     <td>#</td>
//     <td>07.02.2026</td>        ← date
//     <td>[promotion logo]</td>
//     <td>
//       <span class="MatchType">Tag Team Match: </span>
//       <span class="MatchCard">Team A defeat Team B (12:34)</span>
//       <div class="MatchEventLine">Event Name - Type @ Venue</div>
//     </td>
//   </tr>
//
// Key parsing rules:
//   - "defeat" or "defeats" keyword = left side wins
//   - "Draw", "Double Count Out", "Time Limit Draw" etc = draw
//   - "vs." without defeat/draw = no result, skip
// TestScrapeWrestler is a public wrapper for testing individual wrestler scrapes
func (s *CagematchScraper) TestScrapeWrestler(cagematchID int) ([]RawMatch, error) {
	matches, err := s.scrapeWrestlerMatches(cagematchID)
	if err != nil {
		return nil, err
	}
	return deduplicateMatches(matches), nil
}

func (s *CagematchScraper) scrapeWrestlerMatches(cagematchID int) ([]RawMatch, error) {
	return s.scrapeWrestlerMatchesSince(cagematchID, time.Time{})
}

// scrapeWrestlerMatchesSince fetches match history pages (newest first) and stops
// once all matches on a page are at or before the cutoff date.
// If cutoff is zero, it scrapes the full history.
func (s *CagematchScraper) scrapeWrestlerMatchesSince(cagematchID int, cutoff time.Time) ([]RawMatch, error) {
	var allMatches []RawMatch
	hasCutoff := !cutoff.IsZero()

	// Paginate through pages of match history (newest first)
	// Cagematch uses &s=0, &s=100, &s=200 etc (100 matches per page)
	for offset := 0; ; offset += 100 {
		url := fmt.Sprintf("%s/?id=2&nr=%d&page=4&s=%d", s.baseURL, cagematchID, offset)
		doc, err := s.fetchPage(url)
		if err != nil {
			return nil, err
		}

		matchesBefore := len(allMatches)

		// Parse matches from this page
		s.parseMatchRows(doc, &allMatches)

		newOnPage := len(allMatches) - matchesBefore
		log.Printf("[cagematch] CM#%d page offset=%d: found %d matches", cagematchID, offset, newOnPage)

		// If first page returns suspiciously few matches AND we're doing a full scrape
		// (no cutoff), retry once. With a cutoff, fewer matches is expected.
		if offset == 0 && newOnPage > 0 && newOnPage < 50 && !hasCutoff {
			log.Printf("[cagematch] ⚠️ CM#%d only got %d matches on page 1 — retrying...", cagematchID, newOnPage)
			time.Sleep(2 * time.Second)
			allMatches = allMatches[:matchesBefore] // reset
			doc2, err2 := s.fetchPage(url)
			if err2 == nil {
				s.parseMatchRows(doc2, &allMatches)
				newOnPage = len(allMatches) - matchesBefore
				log.Printf("[cagematch] CM#%d retry got %d matches", cagematchID, newOnPage)
			}
		}

		// Stop if this page had no matches (we've gone past the last page)
		if newOnPage == 0 {
			break
		}

		// If we have a cutoff, check if we've gone past it.
		// Cagematch lists newest first, so if the OLDEST match on this page
		// (the last one we just parsed) is before our cutoff, we're done.
		if hasCutoff && newOnPage > 0 {
			oldestOnPage := allMatches[len(allMatches)-1].Date
			if !oldestOnPage.IsZero() && oldestOnPage.Before(cutoff) {
				log.Printf("[cagematch] CM#%d reached cutoff %s — stopping pagination",
					cagematchID, cutoff.Format("2006-01-02"))
				break
			}
		}

		// Check if there's a next page — look for navigation link with next offset
		nextOffset := fmt.Sprintf("s=%d", offset+100)
		hasNext := false
		doc.Find(".NavigationPartPage a").Each(func(i int, a *goquery.Selection) {
			href, exists := a.Attr("href")
			if exists && strings.Contains(href, nextOffset) {
				hasNext = true
			}
		})

		if !hasNext {
			break // no more pages
		}

		time.Sleep(s.delay) // respect rate limit between pages
	}

	log.Printf("[cagematch] Total: %d matches for CM#%d across all pages", len(allMatches), cagematchID)
	return allMatches, nil
}

// parseMatchRows extracts matches from a single page and appends to the slice.
func (s *CagematchScraper) parseMatchRows(doc *goquery.Document, matches *[]RawMatch) {
	doc.Find("tr").Each(func(i int, row *goquery.Selection) {
		// Skip rows without a MatchCard
		card := row.Find(".MatchCard")
		if card.Length() == 0 {
			return
		}

		// Extract date from the second <td> (format: DD.MM.YYYY)
		dateText := ""
		row.Find("td").Each(func(j int, td *goquery.Selection) {
			text := strings.TrimSpace(td.Text())
			// Date cells match DD.MM.YYYY pattern
			if len(text) == 10 && text[2] == '.' && text[5] == '.' {
				dateText = text
			}
		})
		matchDate := parseCagematchDate(dateText)

		// Get the full match text
		matchText := strings.TrimSpace(card.Text())

		// Get match type from sibling span
		matchType := strings.TrimSpace(row.Find(".MatchType").Text())
		// Remove trailing colon and space that Cagematch adds
		matchType = strings.TrimSuffix(matchType, ": ")
		matchType = strings.TrimSuffix(matchType, ":")

		// Get event info
		eventLine := strings.TrimSpace(row.Find(".MatchEventLine").Text())

		// Extract Cagematch event ID from event link (?id=1&nr=XXXXX)
		cagematchEventID := 0
		row.Find(".MatchEventLine a").Each(func(j int, a *goquery.Selection) {
			href, exists := a.Attr("href")
			if exists {
				eid := extractCagematchID(href, 1)
				if eid > 0 {
					cagematchEventID = eid
				}
			}
		})

		// Extract match index (row number — first <td> text)
		matchIndex := 0
		firstTD := row.Find("td").First()
		if firstTD.Length() > 0 {
			idx, err := strconv.Atoi(strings.TrimSpace(firstTD.Text()))
			if err == nil {
				matchIndex = idx
			}
		}

		// Extract all wrestler links from this row's match card
		var participants []matchParticipantRaw
		linkedNames := make(map[string]bool) // track linked names to find unlinked ones
		card.Find("a").Each(func(j int, a *goquery.Selection) {
			href, exists := a.Attr("href")
			if !exists {
				return
			}
			// Only wrestler links (id=2), not stables (id=29)
			cmID := extractCagematchID(href, 2)
			if cmID > 0 {
				name := strings.TrimSpace(a.Text())
				participants = append(participants, matchParticipantRaw{
					Name:        name,
					CagematchID: cmID,
				})
				linkedNames[name] = true
			}
		})

		// Extract unlinked wrestler names (plain text names without <a> tags).
		// These appear in old matches where some wrestlers don't have Cagematch profiles.
		// We add them as ghost participants (CagematchID=0) so the match isn't dropped.
		if len(participants) >= 1 {
			// Get the full card text and try to find names that aren't linked
			// Strategy: look for text nodes that contain names separated by defeat/vs/& keywords
			// but aren't part of any <a> tag
			cardHTML, _ := card.Html()
			// Extract plain text segments (not inside <a> tags)
			// Remove all <a>...</a> tags and see what named text remains
			aTagRegex := regexp.MustCompile(`<a[^>]*>.*?</a>`)
			plainText := aTagRegex.ReplaceAllString(cardHTML, "|||")
			// Split on known separators
			separators := []string{" &amp; ", " & ", ", ", " and "}
			for _, sep := range separators {
				plainText = strings.ReplaceAll(plainText, sep, "|||")
			}
			// Also split on defeat/vs keywords
			for _, kw := range []string{" defeats ", " defeat ", " vs. ", " - "} {
				plainText = strings.ReplaceAll(plainText, kw, "|||")
			}
			// Clean up HTML entities and tags
			plainText = strings.ReplaceAll(plainText, "&amp;", "&")
			tagRegex := regexp.MustCompile(`<[^>]*>`)
			plainText = tagRegex.ReplaceAllString(plainText, "")

			for _, segment := range strings.Split(plainText, "|||") {
				name := strings.TrimSpace(segment)
				// Filter out non-name text: empty, match times like "(12:34)", result text, brackets
				if name == "" || name == "c" || name == "(c)" {
					continue
				}
				if strings.HasPrefix(name, "(") || strings.HasPrefix(name, "[") {
					continue
				}
				// Skip if it looks like a match time or result annotation
				if matched, _ := regexp.MatchString(`^\(?\d+:\d{2}\)?$`, name); matched {
					continue
				}
				if matched, _ := regexp.MatchString(`^\[.*\]$`, name); matched {
					continue
				}
				// Skip common non-name tokens
				skipTokens := []string{"Draw", "Time Limit Draw", "Double Count Out",
					"No Contest", "Double Disqualification", "TITLE CHANGE",
					"Pinfall", "Submission", "Count Out", "Double DQ", "Double KO", "Majority Draw"}
				isSkip := false
				for _, tok := range skipTokens {
					if strings.EqualFold(name, tok) {
						isSkip = true
						break
					}
				}
				if isSkip {
					continue
				}
				// Must look like a name: at least 2 chars, not already linked
				if len(name) < 2 || linkedNames[name] {
					continue
				}
				// Add as ghost participant
				participants = append(participants, matchParticipantRaw{
					Name:        name,
					CagematchID: 0,
				})
			}
		}

		if len(participants) < 2 {
			return // need at least 2 wrestlers
		}

		// Parse the result
		rawMatch, err := parseMatchResult(matchText, matchType, eventLine, participants)
		if err != nil {
			return // skip unparseable matches
		}
		rawMatch.Date = matchDate
		rawMatch.CagematchEventID = cagematchEventID
		rawMatch.MatchIndex = matchIndex

		*matches = append(*matches, *rawMatch)
	})
}

// --- Parsing Helpers ---

type matchParticipantRaw struct {
	Name        string
	CagematchID int
}

// parseMatchResult takes the raw text of a match and figures out who won.
//
// Cagematch uses several result formats:
//   "Suzu Suzuki defeats Risa Sera (25:09)"           → Suzu wins (note: "defeats" with s)
//   "Team A defeat Team B (12:34)"                    → Team A wins
//   "Team A defeat Team B and Team C (8:01)"          → Team A wins multi-way
//   "Team A vs. Team B - Draw (19:07)"                → draw
//   "Team A vs. Team B - Double Count Out (16:27)"    → draw
//   "Team A vs. Team B - Time Limit Draw (60:00)"     → draw
//   "Team A defeat Team B by Count Out (11:34)"       → Team A wins
func parseMatchResult(text, matchType, eventLine string, participants []matchParticipantRaw) (*RawMatch, error) {
	// Detect result type
	// "defeats" must be checked before "defeat" since it contains it
	hasDefeat := false
	defeatKeyword := ""
	for _, kw := range []string{" defeats ", " defeat "} {
		if strings.Contains(text, kw) {
			hasDefeat = true
			defeatKeyword = kw
			break
		}
	}

	// Draw detection — multiple formats
	hasDraw := false
	drawKeywords := []string{"- Draw", "- Double Count Out", "- Time Limit Draw",
		"- Double Disqualification", "- Double DQ", "- No Contest", "- No Decision",
		"- Double KO", "- Majority Draw"}
	for _, dk := range drawKeywords {
		if strings.Contains(text, dk) {
			hasDraw = true
			break
		}
	}

	if !hasDefeat && !hasDraw {
		return nil, fmt.Errorf("no result found")
	}

	// Parse event name from event line
	// Format: "Event Name - Show Type @ Venue in Location"
	eventName := eventLine
	venue := ""
	location := ""
	if atIdx := strings.Index(eventLine, " @ "); atIdx > 0 {
		eventName = strings.TrimSpace(eventLine[:atIdx])
		// Remove show type suffix
		showTypes := []string{" - Pay Per View", " - Online Stream", " - TV-Show",
			" - House Show", " - Live Event", " - Event", " - Premium Live Event"}
		for _, st := range showTypes {
			eventName = strings.TrimSuffix(eventName, st)
		}
		// Parse venue and location from after " @ "
		// Format: "Venue in City, Country" or just "Venue"
		venueLocation := strings.TrimSpace(eventLine[atIdx+3:])
		if inIdx := strings.LastIndex(venueLocation, " in "); inIdx > 0 {
			venue = strings.TrimSpace(venueLocation[:inIdx])
			location = strings.TrimSpace(venueLocation[inIdx+4:])
		} else {
			venue = venueLocation
		}
	}

	// Extract stipulation from match type (e.g. "Singles Match - No DQ" → stipulation = "No DQ")
	stipulation := ""
	normalizedType := normalizeMatchType(matchType)
	if dashIdx := strings.Index(matchType, " - "); dashIdx > 0 {
		stipulation = strings.TrimSpace(matchType[dashIdx+3:])
	}

	// Extract promotion from event line — first part before " - " if it exists before "@"
	promotion := ""
	if colonIdx := strings.Index(eventLine, ": "); colonIdx > 0 && (strings.Index(eventLine, " @ ") < 0 || colonIdx < strings.Index(eventLine, " @ ")) {
		promotion = strings.TrimSpace(eventLine[:colonIdx])
	}

	match := &RawMatch{
		MatchType:   normalizedType,
		EventName:   eventName,
		IsDraw:      hasDraw,
		Venue:       venue,
		Location:    location,
		Promotion:   promotion,
		Stipulation: stipulation,
	}

	if hasDefeat {
		// Split on the defeat keyword — left side wins
		parts := strings.SplitN(text, defeatKeyword, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("couldn't split on defeat keyword")
		}

		winnerText := parts[0]

		// Multi-way matches use "and" between losers:
		//   "Team A defeat Team B and Team C (8:01)"
		// The winner is always on the left of "defeat"

		for _, p := range participants {
			isWinner := strings.Contains(winnerText, p.Name)
			team := 2
			if isWinner {
				team = 1
			}
			match.Participants = append(match.Participants, RawParticipant{
				Name:        p.Name,
				CagematchID: p.CagematchID,
				Team:        team,
				IsWinner:    isWinner,
			})
		}
	} else if hasDraw {
		// Draw — split on " vs. " to assign teams
		vsIdx := strings.Index(text, " vs. ")
		if vsIdx < 0 {
			// Some draws might use different formats, assign all to team 1
			for _, p := range participants {
				match.Participants = append(match.Participants, RawParticipant{
					Name:        p.Name,
					CagematchID: p.CagematchID,
					Team:        1,
					IsWinner:    false,
				})
			}
		} else {
			team1Text := text[:vsIdx]
			for _, p := range participants {
				team := 2
				if strings.Contains(team1Text, p.Name) {
					team = 1
				}
				match.Participants = append(match.Participants, RawParticipant{
					Name:        p.Name,
					CagematchID: p.CagematchID,
					Team:        team,
					IsWinner:    false,
				})
			}
		}
	}

	// Title match detection
	match.IsTitleMatch = strings.Contains(matchType, "Title")

	// Extract match time — pattern: "(MM:SS)" or "(H:MM:SS)" at end of text
	timeRegex := regexp.MustCompile(`\((\d{1,2}:\d{2}(?::\d{2})?)\)\s*$`)
	if tm := timeRegex.FindStringSubmatch(text); len(tm) > 1 {
		match.MatchTime = tm[1]
	}

	// Extract finish type
	if hasDefeat {
		// Check for "by X" patterns before the time
		byRegex := regexp.MustCompile(`by\s+([\w\s]+?)(?:\s*\(\d)`)
		if bm := byRegex.FindStringSubmatch(text); len(bm) > 1 {
			match.FinishType = strings.TrimSpace(bm[1])
		} else {
			match.FinishType = "Pinfall" // default for defeats without "by X"
		}
	} else if hasDraw {
		for _, dk := range []string{"Time Limit Draw", "Double Count Out", "Double Disqualification",
			"Double DQ", "No Contest", "No Decision", "Double KO", "Majority Draw"} {
			if strings.Contains(text, dk) {
				match.FinishType = dk
				break
			}
		}
		if match.FinishType == "" {
			match.FinishType = "Draw"
		}
	}

	return match, nil
}

// extractCagematchID pulls the numeric ID from a Cagematch URL.
// e.g. "?id=2&nr=20600&name=Suzu+Suzuki" with wantedID=2 → 20600
//
// We check the id= param to distinguish wrestlers (id=2) from stables (id=29).
func extractCagematchID(href string, wantedID int) int {
	// Check if this link is the type we want
	// Must use word boundary — "id=2" must NOT match "id=29"
	idPattern := regexp.MustCompile(fmt.Sprintf(`id=%d(&|$)`, wantedID))
	if !idPattern.MatchString(href) {
		return 0
	}

	// Extract the nr= value
	re := regexp.MustCompile(`nr=(\d+)`)
	matches := re.FindStringSubmatch(href)
	if len(matches) < 2 {
		return 0
	}

	id, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return id
}

// normalizeMatchType converts Cagematch's match type strings to our format.
func normalizeMatchType(cmType string) string {
	cmType = strings.ToLower(strings.TrimSpace(cmType))

	switch {
	case strings.Contains(cmType, "singles"):
		return "singles"
	case strings.Contains(cmType, "three way") || strings.Contains(cmType, "triple threat"):
		return "triple_threat"
	case strings.Contains(cmType, "four way") || strings.Contains(cmType, "fatal four"):
		return "4way"
	case strings.Contains(cmType, "ten man") || strings.Contains(cmType, "ten woman"):
		return "5v5"
	case strings.Contains(cmType, "eight man") || strings.Contains(cmType, "eight woman"):
		return "4v4"
	case strings.Contains(cmType, "six man") || strings.Contains(cmType, "six woman"):
		return "3v3"
	case strings.Contains(cmType, "tag team"):
		return "tag"
	default:
		return cmType // return as-is for unknown types
	}
}

// parseCagematchDate parses dates like "01.04.1998" or "April 1, 1998"
func parseCagematchDate(s string) time.Time {
	s = strings.TrimSpace(s)

	// Try DD.MM.YYYY format first (most common on Cagematch)
	if t, err := time.Parse("02.01.2006", s); err == nil {
		return t
	}

	// Try "January 2, 2006" format
	if t, err := time.Parse("January 2, 2006", s); err == nil {
		return t
	}

	// Try year only
	if len(s) == 4 {
		if year, err := strconv.Atoi(s); err == nil {
			return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
		}
	}

	return time.Time{} // zero time if unparseable
}

// guessSocialName figures out what platform a URL belongs to.
func guessSocialName(url string) string {
	url = strings.ToLower(url)
	switch {
	case strings.Contains(url, "twitter.com") || strings.Contains(url, "x.com"):
		return "twitter"
	case strings.Contains(url, "instagram.com"):
		return "instagram"
	case strings.Contains(url, "youtube.com"):
		return "youtube"
	case strings.Contains(url, "tiktok.com"):
		return "tiktok"
	default:
		return "website"
	}
}

// --- Deduplication ---

// deduplicateMatches removes duplicate matches.
// The same match shows up in every participant's history, so we'll see it N times.
// We deduplicate by creating a hash of: event name + sorted participant names.
func deduplicateMatches(matches []RawMatch) []RawMatch {
	seen := make(map[string]bool)
	var unique []RawMatch

	for _, m := range matches {
		key := matchKey(m)
		if !seen[key] {
			seen[key] = true
			unique = append(unique, m)
		}
	}

	return unique
}

// matchKey creates a unique string key for a match.
func matchKey(m RawMatch) string {
	var names []string
	for _, p := range m.Participants {
		names = append(names, p.Name)
	}
	// Sort names so order doesn't matter
	// (simple bubble sort — these arrays are tiny)
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[i] > names[j] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	// Use Cagematch event ID + match index if available (natural unique key)
	if m.CagematchEventID > 0 && m.MatchIndex > 0 {
		return fmt.Sprintf("cm:%d-%d", m.CagematchEventID, m.MatchIndex)
	}
	return fmt.Sprintf("%s|%s|%s|%s", m.EventName, m.Date.Format("2006-01-02"), m.MatchType, strings.Join(names, ","))
}
