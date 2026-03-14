package scraper

import (
	"log"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"joshi-rankings-api/models"
	"joshi-rankings-api/tasklog"
	"joshi-rankings-api/services"
)

// isMultiFallType returns true for match types that Cagematch splits into
// individual fall entries (gauntlets, battle royals, rumbles).
// We only want to store ONE entry per event+type combo for these.
func isMultiFallType(matchType string) bool {
	mt := strings.ToLower(matchType)
	return strings.Contains(mt, "gauntlet") ||
		strings.Contains(mt, "battle royal") ||
		strings.Contains(mt, "royal rumble") ||
		strings.Contains(mt, "rumble")
}

// Processor handles two separate jobs:
//
// Phase 1 — COLLECTION (CollectMatches):
//   Scrape matches → discover wrestlers → store in DB
//   NO ELO calculation happens here
//
// Phase 2 — CALCULATION (RecalculateAllELO):
//   Load ALL matches from DB → sort by date → walk through
//   chronologically calculating ELO from scratch
//
// Why separate? ELO is order-dependent. Match #100's result depends
// on every match before it. If we calculate as we scrape, the order
// is per-wrestler (not chronological) and ELO will be wrong.
//
// Python analogy:
//   phase 1: scrape_and_store()   # just data collection
//   phase 2: recalculate_elo()    # reset + replay all matches in order
type Processor struct {
	db      *gorm.DB
	scraper *CagematchScraper
	calc    services.ELOCalculator
}

func NewProcessor(db *gorm.DB, scraper *CagematchScraper) *Processor {
	return &Processor{
		db:      db,
		scraper: scraper,
		calc:    services.NewELOCalculator(),
	}
}

// ========================================
// PHASE 1: COLLECTION — store matches, no ELO
// ========================================

// CollectMatches takes raw scraped matches and stores them in the DB.
// It discovers new wrestlers (gender-checked) but does NOT calculate ELO.
// Returns the number of new matches stored.
func (p *Processor) CollectMatches(rawMatches []RawMatch) int {
	newCount := 0
	log.Printf("[collector] Received %d raw matches to process", len(rawMatches))

	for i, raw := range rawMatches {
		// Check if match already exists
		existingMatchID := p.findExistingMatch(raw)
		if existingMatchID > 0 {
			// Match exists — but check if we need to add missing participants
			added := p.addMissingParticipants(existingMatchID, raw)
			if added > 0 {
				log.Printf("[collector] Match %d exists, added %d missing participants: %s", i, added, raw.EventName)
			}
			continue
		}

		// Resolve all participants to DB wrestlers
		// This might trigger profile scrapes for unknown wrestlers
		wrestlers, skip := p.resolveParticipants(raw)
		if skip {
			continue
		}

		// Store the match WITHOUT ELO calculation
		err := p.storeMatch(raw, wrestlers)
		if err != nil {
			log.Printf("[collector] Error storing match (%s): %v", raw.EventName, err)
			continue
		}

		newCount++
		log.Printf("[collector] Stored match: %s — %s", raw.EventName, raw.MatchType)
	}

	return newCount
}

// storeMatch saves a match and its participants to the DB.
// NO ELO calculation — just raw data storage.
func (p *Processor) storeMatch(raw RawMatch, wrestlers map[int]models.Wrestler) error {
	return p.db.Transaction(func(tx *gorm.DB) error {
		match := models.Match{
			MatchType:        raw.MatchType,
			EventName:        raw.EventName,
			Date:             raw.Date,
			IsTitleMatch:     raw.IsTitleMatch,
			IsDraw:           raw.IsDraw,
			MatchTime:        raw.MatchTime,
			FinishType:       raw.FinishType,
			Venue:            raw.Venue,
			Location:         raw.Location,
			Promotion:        raw.Promotion,
			Stipulation:      raw.Stipulation,
			CagematchEventID: raw.CagematchEventID,
			MatchIndex:       raw.MatchIndex,
			MatchKey:         BuildMatchKey(raw),
		}

		if err := tx.Create(&match).Error; err != nil {
			return err
		}

		// Create participants — tracked wrestlers get WrestlerID, ghosts get name only
		for _, rp := range raw.Participants {
			wrestler, ok := wrestlers[rp.CagematchID]
			if ok {
				// Tracked wrestler
				participant := models.MatchParticipant{
					MatchID:    match.ID,
					WrestlerID: wrestler.ID,
					Team:       rp.Team,
					IsWinner:   rp.IsWinner,
				}
				if err := tx.Create(&participant).Error; err != nil {
					return err
				}
			} else {
				// Ghost participant (male/untracked)
				participant := models.MatchParticipant{
					MatchID:          match.ID,
					WrestlerID:       0,
					Team:             rp.Team,
					IsWinner:         rp.IsWinner,
					GhostName:        rp.Name,
					GhostCagematchID: rp.CagematchID,
				}
				if err := tx.Create(&participant).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})
}

// ========================================
// PHASE 2: CALCULATION — chronological ELO
// ========================================

// RecalculateAllELO resets every wrestler to StartingELO and replays ALL matches
// in chronological order, calculating ELO step by step.
//
// This is the "nuclear option" — it recalculates everything from scratch.
// Call this after the initial big scrape, or whenever you want to fix ELO.
//
// The algorithm:
//   1. Reset all wrestlers: ELO=StartingELO, Wins=0, Losses=0, Draws=0
//   2. Delete all ELO history entries
//   3. Load ALL matches sorted by date ASC (oldest first)
//   4. For each match, calculate ELO changes and apply them
//
// This guarantees correct ELO regardless of what order matches were scraped.
func (p *Processor) RecalculateAllELO() error {
	log.Println("[elo] Starting full ELO recalculation...")

	// Step 1: Reset all wrestlers to starting state
	result := p.db.Model(&models.Wrestler{}).Where("1 = 1").Updates(map[string]interface{}{
		"elo":         services.StartingELO,
		"match_count": 0,
		"wins":        0,
		"losses":      0,
		"draws":       0,
	})
	if result.Error != nil {
		return result.Error
	}
	log.Printf("[elo] Reset %d wrestlers to ELO %.0f", result.RowsAffected, services.StartingELO)
	tasklog.Infof("Reset %d wrestlers to ELO %.0f", result.RowsAffected, services.StartingELO)

	// Step 2: Clear all ELO and momentum history
	p.db.Where("1 = 1").Delete(&models.ELOHistory{})
	p.db.Where("1 = 1").Delete(&models.MomentumHistory{})
	log.Println("[elo] Cleared ELO and momentum history")

	// Step 3: Load ALL matches sorted oldest first
	var matches []models.Match
	p.db.Order("date ASC, id ASC").Find(&matches)
	log.Printf("[elo] Loaded %d matches, now loading participants...", len(matches))
	tasklog.Infof("Loaded %d matches, loading participants...", len(matches))

	// Load participants in batches (Preload chokes on 400k+ matches)
	// Build a map of matchID → []MatchParticipant
	participantMap := make(map[uint][]models.MatchParticipant)
	batchSize := 10000
	matchIDs := make([]uint, len(matches))
	for i, m := range matches {
		matchIDs[i] = m.ID
	}
	for i := 0; i < len(matchIDs); i += batchSize {
		end := i + batchSize
		if end > len(matchIDs) {
			end = len(matchIDs)
		}
		var participants []models.MatchParticipant
		p.db.Where("match_id IN ?", matchIDs[i:end]).Find(&participants)
		for _, mp := range participants {
			participantMap[mp.MatchID] = append(participantMap[mp.MatchID], mp)
		}
	}
	// Attach participants to matches
	for i := range matches {
		matches[i].Participants = participantMap[matches[i].ID]
	}
	log.Printf("[elo] Processing %d matches chronologically...", len(matches))

	// Step 4: Walk through each match and calculate ELO
	//
	// We keep an in-memory map of wrestler ELOs so we don't have to
	// hit the DB for every single calculation. We write back at the end.
	//
	// Python equivalent:
	//   elos = {w.id: StartingELO for w in wrestlers}
	//   for match in sorted_matches:
	//       calculate_and_update(elos, match)
	elos := make(map[uint]float64)        // wrestler ID → current ELO
	matchCounts := make(map[uint]int)     // wrestler ID → total match count
	wins := make(map[uint]int)            // wrestler ID → win count
	losses := make(map[uint]int)          // wrestler ID → loss count
	draws := make(map[uint]int)           // wrestler ID → draw count
	lastMatch := make(map[uint]time.Time) // wrestler ID → last match date
	eloChanges := make(map[uint][]float64) // wrestler ID → sliding window of last 10 ELO changes
	momentums := make(map[uint]float64)    // wrestler ID → current momentum

	// Initialize all wrestlers at StartingELO
	var allWrestlers []models.Wrestler
	p.db.Find(&allWrestlers)
	for _, w := range allWrestlers {
		elos[w.ID] = services.StartingELO
	}

	// Process each match — collect ELO and momentum history in memory, batch write later
	var eloHistoryBatch []models.ELOHistory
	var momentumHistoryBatch []models.MomentumHistory
	var matchParticipantUpdates []models.MatchParticipant

	for i, match := range matches {
		p.processMatchELO(match, elos, matchCounts, wins, losses, draws, &eloHistoryBatch, eloChanges, momentums, &momentumHistoryBatch, &matchParticipantUpdates)

		// Track last match date for each participant
		for _, mp := range match.Participants {
			if match.Date.After(lastMatch[mp.WrestlerID]) {
				lastMatch[mp.WrestlerID] = match.Date
			}
		}

		// Progress log every 10000 matches
		if (i+1)%10000 == 0 {
			log.Printf("[elo] Processed %d/%d matches", i+1, len(matches))
			tasklog.Infof("Processed %d/%d matches", i+1, len(matches))
		}
	}

	log.Printf("[elo] Writing %d ELO history entries and %d momentum history entries...", len(eloHistoryBatch), len(momentumHistoryBatch))

	// Step 5: Write all final ELOs + momentum + history back to DB in batched transactions
	err := p.db.Transaction(func(tx *gorm.DB) error {
		for wrestlerID, elo := range elos {
			updates := map[string]interface{}{
				"elo":         elo,
				"momentum":    momentums[wrestlerID],
				"match_count": matchCounts[wrestlerID],
				"wins":        wins[wrestlerID],
				"losses":      losses[wrestlerID],
				"draws":       draws[wrestlerID],
			}
			if lm, ok := lastMatch[wrestlerID]; ok && !lm.IsZero() {
				updates["last_match_date"] = lm
			}
			tx.Model(&models.Wrestler{}).Where("id = ?", wrestlerID).Updates(updates)
		}
		return nil
	})

	// Batch insert ELO history in chunks of 5000
	totalHistory := len(eloHistoryBatch)
	for i := 0; i < totalHistory; i += 5000 {
		end := i + 5000
		if end > totalHistory {
			end = totalHistory
		}
		p.db.CreateInBatches(eloHistoryBatch[i:end], 5000)
		if (i/5000+1)%20 == 0 {
			log.Printf("[elo] Written %d/%d ELO history entries...", end, totalHistory)
		}
	}
	log.Printf("[elo] All %d ELO history entries written", totalHistory)

	// Batch insert momentum history in chunks of 5000
	totalMomentumHistory := len(momentumHistoryBatch)
	for i := 0; i < totalMomentumHistory; i += 5000 {
		end := i + 5000
		if end > totalMomentumHistory {
			end = totalMomentumHistory
		}
		p.db.CreateInBatches(momentumHistoryBatch[i:end], 5000)
		if (i/5000+1)%20 == 0 {
			log.Printf("[elo] Written %d/%d momentum history entries...", end, totalMomentumHistory)
		}
	}
	log.Printf("[elo] All %d momentum history entries written", totalMomentumHistory)

	// Batch update match participants with ELO changes
	log.Printf("[elo] Updating ELO changes for %d match participants...", len(matchParticipantUpdates))
	for i := 0; i < len(matchParticipantUpdates); i += 1000 {
		end := i + 1000
		if end > len(matchParticipantUpdates) {
			end = len(matchParticipantUpdates)
		}
		for j := i; j < end; j++ {
			mp := matchParticipantUpdates[j]
			p.db.Model(&models.MatchParticipant{}).Where("id = ?", mp.ID).Update("elo_change", mp.EloChange)
		}
	}
	log.Printf("[elo] All match participant ELO changes updated")
	if err != nil {
		return err
	}

	log.Printf("[elo] Recalculation complete! Processed %d matches for %d wrestlers", len(matches), len(elos))
	tasklog.Successf("Recalculation complete! Processed %d matches for %d wrestlers", len(matches), len(elos))

	// Re-assign current promotions using tiered logic
	p.UpdateAllPromotions()

	return nil
}

// processMatchELO calculates ELO for a single match and updates the in-memory maps.
// Also calculates momentum and saves ELO/momentum history entries for each wrestler.
func (p *Processor) processMatchELO(
	match models.Match,
	elos map[uint]float64,
	matchCounts, wins, losses, draws map[uint]int,
	eloHistory *[]models.ELOHistory,
	eloChanges map[uint][]float64,
	momentums map[uint]float64,
	momentumHistory *[]models.MomentumHistory,
	matchParticipantUpdates *[]models.MatchParticipant,
) {
	// Build temporary wrestler objects with current ELO from our map
	// The ELO calculator needs Wrestler structs with .ELO set
	var winners, losers []models.Wrestler
	var team1, team2 []models.Wrestler

	for _, participant := range match.Participants {
		// Skip ghost participants — no ELO impact
		if participant.WrestlerID == 0 {
			continue
		}
		w := models.Wrestler{
			ID:  participant.WrestlerID,
			ELO: elos[participant.WrestlerID],
		}

		if match.IsDraw {
			if participant.Team == 1 {
				team1 = append(team1, w)
			} else {
				team2 = append(team2, w)
			}
		} else {
			if participant.IsWinner {
				winners = append(winners, w)
			} else {
				losers = append(losers, w)
			}
		}
	}

	// Count match + W/L/D for ALL participants (even ghost matches with missing opponents)
	for _, participant := range match.Participants {
		if participant.WrestlerID == 0 {
			continue
		}
		matchCounts[participant.WrestlerID]++
		if match.IsDraw {
			draws[participant.WrestlerID]++
		} else if participant.IsWinner {
			wins[participant.WrestlerID]++
		} else {
			losses[participant.WrestlerID]++
		}
	}

	// Calculate ELO changes (need both sides for this)
	var eloResults []services.ELOResult
	if match.IsDraw {
		if len(team1) == 0 || len(team2) == 0 {
			return // can't calc ELO without both teams
		}
		eloResults = p.calc.CalculateMulti(team1, team2, match.IsTitleMatch, true)
	} else {
		if len(winners) == 0 || len(losers) == 0 {
			return // can't calc ELO without both sides
		}
		eloResults = p.calc.CalculateMulti(winners, losers, match.IsTitleMatch, false)
	}

	// Map results by wrestler ID for easy lookup
	resultsMap := make(map[uint]services.ELOResult)
	for _, r := range eloResults {
		resultsMap[r.WrestlerID] = r
	}

	// Apply results to our in-memory maps
	for _, r := range eloResults {
		elos[r.WrestlerID] = r.NewELO
		eloChange := r.NewELO - r.OldELO

		// Update sliding window of ELO changes (last 10)
		if eloChanges[r.WrestlerID] == nil {
			eloChanges[r.WrestlerID] = make([]float64, 0, 10)
		}
		eloChanges[r.WrestlerID] = append(eloChanges[r.WrestlerID], eloChange)
		if len(eloChanges[r.WrestlerID]) > 10 {
			eloChanges[r.WrestlerID] = eloChanges[r.WrestlerID][1:] // Remove oldest
		}

		// Calculate momentum based on weighted recent ELO changes
		momentum := calculateMomentum(eloChanges[r.WrestlerID])
		momentums[r.WrestlerID] = momentum

		// Append ELO history entry (batch written later)
		*eloHistory = append(*eloHistory, models.ELOHistory{
			WrestlerID: r.WrestlerID,
			ELO:        r.NewELO,
			MatchID:    match.ID,
			MatchDate:  match.Date,
		})

		// Append momentum history entry (batch written later)
		*momentumHistory = append(*momentumHistory, models.MomentumHistory{
			WrestlerID: r.WrestlerID,
			Momentum:   momentum,
			MatchID:    match.ID,
			MatchDate:  match.Date,
		})
	}

	// Store ELO changes for match participants
	for _, participant := range match.Participants {
		if result, exists := resultsMap[participant.WrestlerID]; exists {
			eloChange := result.NewELO - result.OldELO
			*matchParticipantUpdates = append(*matchParticipantUpdates, models.MatchParticipant{
				ID:        participant.ID,
				EloChange: eloChange,
			})
		}
	}
}

// calculateMomentum calculates weighted momentum from recent ELO changes
// Formula: Sum of (change * weight) where weight = position (10 for most recent, 9 for second, etc.)
// Normalized to -100 to +100 scale
func calculateMomentum(changes []float64) float64 {
	if len(changes) == 0 {
		return 0.0
	}

	weightedSum := 0.0
	totalWeight := 0.0

	// Weight recent changes more heavily (most recent = weight 10, second = 9, etc.)
	for i, change := range changes {
		weight := float64(i + 1) // 1 for oldest, up to 10 for most recent
		weightedSum += change * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return 0.0
	}

	// Calculate weighted average
	weightedAvg := weightedSum / totalWeight

	// Normalize to -100 to +100 scale
	// Assuming typical ELO changes are in the range of -200 to +200
	// Scale factor of 0.5 converts this to -100 to +100 range
	momentum := weightedAvg * 0.5

	// Clamp to -100 to +100 range
	if momentum > 100 {
		momentum = 100
	} else if momentum < -100 {
		momentum = -100
	}

	return momentum
}

// ========================================
// INCREMENTAL MODE — for ongoing 6hr cron
// ========================================

// ProcessNewMatches handles the ongoing case: new matches come in,
// we store them AND calculate ELO immediately.
//
// This is fine for incremental updates because:
// - All new matches are recent (within hours of each other)
// - Order barely matters when dates are the same
// - Everyone's ELO is already correct from the initial recalculation
//
// For the initial scrape, use CollectMatches + RecalculateAllELO instead.
func (p *Processor) ProcessNewMatches(rawMatches []RawMatch) int {
	newCount := 0

	// Sort by date just to be safe
	sort.Slice(rawMatches, func(i, j int) bool {
		return rawMatches[i].Date.Before(rawMatches[j].Date)
	})

	for _, raw := range rawMatches {
		if p.findExistingMatch(raw) > 0 {
			continue
		}

		wrestlers, skip := p.resolveParticipants(raw)
		if skip {
			continue
		}

		// Store AND calculate ELO in one transaction
		err := p.storeAndCalculateMatch(raw, wrestlers)
		if err != nil {
			log.Printf("[processor] Error processing match (%s): %v", raw.EventName, err)
			continue
		}

		newCount++
		log.Printf("[processor] Processed match: %s — %s", raw.EventName, raw.MatchType)
	}

	return newCount
}

// storeAndCalculateMatch saves a match AND updates ELO in one transaction.
// Used for incremental updates (ongoing cron), NOT initial scrape.
func (p *Processor) storeAndCalculateMatch(raw RawMatch, wrestlers map[int]models.Wrestler) error {
	return p.db.Transaction(func(tx *gorm.DB) error {
		// Create the match
		match := models.Match{
			MatchType:        raw.MatchType,
			EventName:        raw.EventName,
			Date:             raw.Date,
			IsTitleMatch:     raw.IsTitleMatch,
			IsDraw:           raw.IsDraw,
			MatchTime:        raw.MatchTime,
			FinishType:       raw.FinishType,
			Venue:            raw.Venue,
			Location:         raw.Location,
			Promotion:        raw.Promotion,
			Stipulation:      raw.Stipulation,
			CagematchEventID: raw.CagematchEventID,
			MatchIndex:       raw.MatchIndex,
			MatchKey:         BuildMatchKey(raw),
		}
		if err := tx.Create(&match).Error; err != nil {
			return err
		}

		// Create participants and split into winners/losers
		var winners, losers []models.Wrestler
		var team1, team2 []models.Wrestler

		for _, rp := range raw.Participants {
			wrestler, ok := wrestlers[rp.CagematchID]
			if ok {
				// Tracked wrestler
				participant := models.MatchParticipant{
					MatchID:    match.ID,
					WrestlerID: wrestler.ID,
					Team:       rp.Team,
					IsWinner:   rp.IsWinner,
				}
				if err := tx.Create(&participant).Error; err != nil {
					return err
				}

				// Only tracked wrestlers participate in ELO
				if raw.IsDraw {
					if rp.Team == 1 {
						team1 = append(team1, wrestler)
					} else {
						team2 = append(team2, wrestler)
					}
				} else if rp.IsWinner {
					winners = append(winners, wrestler)
				} else {
					losers = append(losers, wrestler)
				}
			} else {
				// Ghost participant — stored but no ELO impact
				participant := models.MatchParticipant{
					MatchID:          match.ID,
					WrestlerID:       0,
					Team:             rp.Team,
					IsWinner:         rp.IsWinner,
					GhostName:        rp.Name,
					GhostCagematchID: rp.CagematchID,
				}
				if err := tx.Create(&participant).Error; err != nil {
					return err
				}
			}
		}

		// Calculate ELO — need both sides, otherwise skip ELO (still store the match)
		var eloResults []services.ELOResult
		if raw.IsDraw {
			if len(team1) > 0 && len(team2) > 0 {
				eloResults = p.calc.CalculateMulti(team1, team2, raw.IsTitleMatch, true)
			}
		} else {
			if len(winners) > 0 && len(losers) > 0 {
				eloResults = p.calc.CalculateMulti(winners, losers, raw.IsTitleMatch, false)
			}
		}

		// Apply ELO changes and calculate momentum
		for _, r := range eloResults {
			eloChange := r.NewELO - r.OldELO

			// Get last 9 ELO changes to calculate momentum with the new one
			var lastChanges []float64
			var eloHistoryEntries []models.ELOHistory
			tx.Where("wrestler_id = ?", r.WrestlerID).Order("match_date DESC").Limit(9).Find(&eloHistoryEntries)
			
			// Extract ELO changes from history (most recent first)
			for i := len(eloHistoryEntries) - 1; i >= 0; i-- {
				entry := eloHistoryEntries[i]
				var prevELO float64 = services.StartingELO // default if no previous entry
				if i > 0 {
					prevELO = eloHistoryEntries[i-1].ELO
				} else {
					// Get the wrestler's ELO before this history entry
					var prevEntry models.ELOHistory
					if tx.Where("wrestler_id = ? AND match_date < ?", r.WrestlerID, entry.MatchDate).Order("match_date DESC").First(&prevEntry).Error == nil {
						prevELO = prevEntry.ELO
					}
				}
				lastChanges = append(lastChanges, entry.ELO - prevELO)
			}
			
			// Add the current change
			lastChanges = append(lastChanges, eloChange)
			
			// Calculate momentum
			momentum := calculateMomentum(lastChanges)

			// Update wrestler with new ELO and momentum
			updates := map[string]interface{}{
				"elo": r.NewELO,
				"momentum": momentum,
			}
			tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).Updates(updates)

			if raw.IsDraw {
				tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).
					Update("draws", gorm.Expr("draws + 1"))
			} else if r.IsWinner {
				tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).
					Update("wins", gorm.Expr("wins + 1"))
			} else {
				tx.Model(&models.Wrestler{}).Where("id = ?", r.WrestlerID).
					Update("losses", gorm.Expr("losses + 1"))
			}

			// Store ELO and momentum history
			tx.Create(&models.ELOHistory{
				WrestlerID: r.WrestlerID,
				ELO:        r.NewELO,
				MatchID:    match.ID,
				MatchDate:  raw.Date,
			})

			tx.Create(&models.MomentumHistory{
				WrestlerID: r.WrestlerID,
				Momentum:   momentum,
				MatchID:    match.ID,
				MatchDate:  raw.Date,
			})

			// Update match participant with ELO change
			tx.Model(&models.MatchParticipant{}).
				Where("match_id = ? AND wrestler_id = ?", match.ID, r.WrestlerID).
				Update("elo_change", eloChange)
		}

		return nil
	})
}

// ========================================
// PROMOTION ASSIGNMENT (tiered)
// ========================================

// DetermineCurrentPromotion figures out a wrestler's current promotion using:
//   Tier 1: If the wrestler's Cagematch profile lists a known promotion (not empty/freelance), use it.
//   Tier 2: Walk backwards through promotion_histories, accumulate up to 50 matches,
//           and pick the promotion with the most matches in that window.
func (p *Processor) DetermineCurrentPromotion(wrestlerID uint) string {
	// Tier 1: check the wrestler's profile-sourced promotion
	var w models.Wrestler
	if err := p.db.Select("promotion").First(&w, wrestlerID).Error; err != nil {
		return ""
	}

	profilePromo := strings.TrimSpace(w.Promotion)
	if profilePromo != "" && !p.isUnknownPromotion(profilePromo) {
		return profilePromo
	}

	// Tier 2: last 20 matches from promotion_histories — need 35%+ to assign
	return p.promotionFromLast20(wrestlerID)
}

// isUnknownPromotion returns true for promotions that mean "we don't know".
// Freelance is a valid status and should be kept as-is.
func (p *Processor) isUnknownPromotion(promo string) bool {
	lower := strings.ToLower(promo)
	return lower == "" ||
		lower == "unknown" ||
		lower == "none"
}

// promotionFromLast20 walks backwards through promotion_histories year by year,
// accumulates match counts until 20, and returns the top promotion only if it
// accounts for 35%+ of those matches. Otherwise returns "Freelance".
func (p *Processor) promotionFromLast20(wrestlerID uint) string {
	var entries []models.PromotionHistory
	p.db.Where("wrestler_id = ?", wrestlerID).
		Order("year DESC, matches DESC").
		Find(&entries)

	if len(entries) == 0 {
		return "Freelance"
	}

	promoCounts := make(map[string]int)
	total := 0
	remaining := 20

	for _, e := range entries {
		if remaining <= 0 {
			break
		}
		take := e.Matches
		if take > remaining {
			take = remaining
		}
		promoCounts[e.Promotion] += take
		total += take
		remaining -= take
	}

	// Pick the promotion with the most matches
	bestPromo := ""
	bestCount := 0
	for promo, count := range promoCounts {
		if count > bestCount {
			bestCount = count
			bestPromo = promo
		}
	}

	// Must be 35%+ of the window to qualify
	if total > 0 && float64(bestCount)/float64(total) >= 0.35 {
		return bestPromo
	}

	return "Freelance"
}

// UpdateAllPromotions runs the tiered promotion assignment for every wrestler.
func (p *Processor) UpdateAllPromotions() {
	log.Println("[promotions] Updating current promotion for all wrestlers...")

	var wrestlers []models.Wrestler
	p.db.Select("id").Where("id > 0").Find(&wrestlers)

	updated := 0
	for _, w := range wrestlers {
		promo := p.DetermineCurrentPromotion(w.ID)
		if promo != "" {
			p.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Update("promotion", promo)
			updated++
		}
	}

	log.Printf("[promotions] Updated promotion for %d wrestlers", updated)
}

// ========================================
// REGION CALCULATION
// ========================================

// UpdateWrestlerRegions calculates current_region and general_region for all wrestlers
// based on their promotion history and the promotions table.
func (p *Processor) UpdateWrestlerRegions() {
	log.Println("[regions] Calculating wrestler regions...")

	var wrestlers []models.Wrestler
	p.db.Where("id > 0").Find(&wrestlers)

	updated := 0
	for _, w := range wrestlers {
		// General region: country of promotion with most total matches
		var generalPromo struct {
			Country string
		}
		p.db.Raw(`
			SELECT pr.country FROM promotion_histories ph
			JOIN promotions pr ON pr.cagematch_id = ph.promotion_id
			WHERE ph.wrestler_id = ? AND pr.country != ''
			GROUP BY pr.country
			ORDER BY SUM(ph.matches) DESC
			LIMIT 1
		`, w.ID).Scan(&generalPromo)

		// Current region: country of promotion with most matches in last 50
		// We approximate by looking at the most recent years in promotion_histories
		var currentPromo struct {
			Country string
		}
		p.db.Raw(`
			SELECT pr.country FROM promotion_histories ph
			JOIN promotions pr ON pr.cagematch_id = ph.promotion_id
			WHERE ph.wrestler_id = ? AND pr.country != ''
			ORDER BY ph.year DESC
			LIMIT 5
		`, w.ID).Scan(&currentPromo)

		// More accurate: get the top country from most recent entries
		p.db.Raw(`
			SELECT pr.country FROM promotion_histories ph
			JOIN promotions pr ON pr.cagematch_id = ph.promotion_id
			WHERE ph.wrestler_id = ? AND pr.country != ''
			AND ph.year >= (SELECT MAX(year) - 1 FROM promotion_histories WHERE wrestler_id = ?)
			GROUP BY pr.country
			ORDER BY SUM(ph.matches) DESC
			LIMIT 1
		`, w.ID, w.ID).Scan(&currentPromo)

		if generalPromo.Country != "" || currentPromo.Country != "" {
			updates := map[string]interface{}{}
			if generalPromo.Country != "" {
				updates["general_region"] = generalPromo.Country
			}
			if currentPromo.Country != "" {
				updates["current_region"] = currentPromo.Country
			}
			if len(updates) > 0 {
				p.db.Model(&models.Wrestler{}).Where("id = ?", w.ID).Updates(updates)
				updated++
			}
		}
	}

	log.Printf("[regions] Updated regions for %d wrestlers", updated)
}

// ========================================
// SHARED HELPERS
// ========================================

// findExistingMatch returns the DB match ID if this match already exists, or 0 if not.
// Uses a deterministic composite key: date|event_name|match_type|sorted_participant_ids
// FindExistingMatch is a public wrapper for testing
func (p *Processor) FindExistingMatch(raw RawMatch) uint {
	return p.findExistingMatch(raw)
}

func (p *Processor) findExistingMatch(raw RawMatch) uint {
	// Multi-fall dedup: gauntlets/royals/rumbles — if we already have ANY
	// match with this event + type on the same date, return it
	if raw.CagematchEventID > 0 && isMultiFallType(raw.MatchType) {
		var match models.Match
		if p.db.Where("cagematch_event_id = ? AND match_type = ?", raw.CagematchEventID, raw.MatchType).First(&match).Error == nil {
			return match.ID
		}
	}

	// Primary dedup: exact match key lookup
	key := BuildMatchKey(raw)
	if key != "" {
		var match models.Match
		if p.db.Where("match_key = ?", key).First(&match).Error == nil {
			return match.ID
		}
	}

	// Secondary dedup: no-time variant.
	// Cagematch often adds match_time after initial upload. If we stored a match
	// before the time was added, the key won't match. Check for a key that
	// matches without time, and if found, upgrade the stored key.
	keyNoTime := BuildMatchKeyNoTime(raw)
	if keyNoTime != "" && raw.MatchTime != "" {
		var match models.Match
		// cm: format no-time key ends with | (empty time)
		oldKey := keyNoTime + "|"
		if p.db.Where("match_key = ?", oldKey).First(&match).Error == nil {
			p.db.Model(&match).Update("match_key", key)
			if match.MatchTime == "" {
				p.db.Model(&match).Update("match_time", raw.MatchTime)
			}
			return match.ID
		}
	}

	// Tertiary dedup: match old-format keys (date|event|type|participants|time)
	// against new cm: format. Catches matches stored before we switched to cm: keys.
	if raw.CagematchEventID > 0 {
		pids := sortedParticipantIDs(raw)
		// Search by event ID + check participants match
		var candidates []models.Match
		p.db.Where("cagematch_event_id = ? AND match_key NOT LIKE 'cm:%'",
			raw.CagematchEventID).Find(&candidates)
		for _, m := range candidates {
			// Check if participants match by comparing sorted IDs from the old key
			if strings.Contains(m.MatchKey, pids) {
				// Upgrade to new key format
				p.db.Model(&m).Update("match_key", key)
				if raw.MatchTime != "" && m.MatchTime == "" {
					p.db.Model(&m).Update("match_time", raw.MatchTime)
				}
				if raw.EventName != "" && raw.EventName != m.EventName {
					p.db.Model(&m).Update("event_name", raw.EventName)
				}
				return m.ID
			}
		}
	}

	// Fallback for legacy matches without match_key: date + event_name + match_type
	// Only matches if ALL incoming participant IDs are present in the existing match
	var candidates []models.Match
	p.db.Where("date = ? AND event_name = ? AND match_key = ''",
		raw.Date, raw.EventName).Find(&candidates)

	rawIDs := make(map[int]bool)
	for _, rp := range raw.Participants {
		if rp.CagematchID > 0 {
			rawIDs[rp.CagematchID] = true
		}
	}

	for _, m := range candidates {
		if strings.ToLower(m.MatchType) != strings.ToLower(raw.MatchType) {
			continue
		}

		var participants []models.MatchParticipant
		p.db.Where("match_id = ?", m.ID).Find(&participants)

		existingIDs := make(map[int]bool)
		for _, mp := range participants {
			var w models.Wrestler
			if err := p.db.Select("cagematch_id").First(&w, mp.WrestlerID).Error; err == nil {
				existingIDs[int(w.CagematchID)] = true
			}
		}

		// All incoming participants must exist in the stored match
		allFound := true
		for id := range rawIDs {
			if !existingIDs[id] {
				allFound = false
				break
			}
		}
		if allFound && len(rawIDs) > 0 {
			return m.ID
		}
	}

	return 0
}

// addMissingParticipants adds any participants to an existing match that
// weren't in the DB when the match was first stored.
func (p *Processor) addMissingParticipants(matchID uint, raw RawMatch) int {
	// Get existing participant wrestler IDs (as cagematch IDs)
	var existing []models.MatchParticipant
	p.db.Where("match_id = ?", matchID).Find(&existing)

	existingCMIDs := make(map[int]bool)
	for _, mp := range existing {
		if mp.GhostCagematchID > 0 {
			existingCMIDs[mp.GhostCagematchID] = true
			continue
		}
		var w models.Wrestler
		if err := p.db.Select("cagematch_id").First(&w, mp.WrestlerID).Error; err == nil {
			existingCMIDs[int(w.CagematchID)] = true
		}
	}

	// Also build a set of existing wrestler IDs (not just CM IDs) to prevent dupes
	existingWrestlerIDs := make(map[uint]bool)
	for _, mp := range existing {
		existingWrestlerIDs[mp.WrestlerID] = true
	}

	added := 0
	for _, rp := range raw.Participants {
		if rp.CagematchID == 0 || existingCMIDs[rp.CagematchID] {
			continue
		}

		// Find or create the wrestler
		var wrestler models.Wrestler
		if err := p.db.Where("cagematch_id = ?", rp.CagematchID).First(&wrestler).Error; err != nil {
			// Wrestler not in DB — skip (they'll be added on a full scrape)
			continue
		}

		// Double-check by wrestler ID (belt + suspenders)
		if existingWrestlerIDs[wrestler.ID] {
			continue
		}

		// Add participant link
		participant := models.MatchParticipant{
			MatchID:    matchID,
			WrestlerID: wrestler.ID,
			Team:       rp.Team,
			IsWinner:   rp.IsWinner,
		}
		if err := p.db.Create(&participant).Error; err == nil {
			added++
			existingWrestlerIDs[wrestler.ID] = true
			log.Printf("[collector] Added %s to existing match #%d", wrestler.Name, matchID)
		}
	}

	return added
}

// resolveParticipants maps raw participants to DB wrestlers.
// Discovers new wrestlers via Cagematch profile scrape + gender filter.
func (p *Processor) resolveParticipants(raw RawMatch) (map[int]models.Wrestler, bool) {
	wrestlers := make(map[int]models.Wrestler)

	for _, rp := range raw.Participants {
		if rp.CagematchID == 0 {
			continue
		}

		// Check DB first
		var wrestler models.Wrestler
		result := p.db.Where("cagematch_id = ?", rp.CagematchID).First(&wrestler)

		if result.Error == nil {
			wrestlers[rp.CagematchID] = wrestler
			continue
		}

		// Check skip list first (persisted male wrestlers)
		if p.scraper.IsSkipped(rp.CagematchID) {
			// Skip this participant, not the whole match
			continue
		}

		// Not in DB or skip list — scrape their profile
		log.Printf("[collector] New wrestler: %s (CM#%d) — checking profile...", rp.Name, rp.CagematchID)

		profile, err := p.scraper.ScrapeWrestlerProfile(rp.CagematchID)
		time.Sleep(p.scraper.GetDelay())

		if err != nil {
			log.Printf("[collector] Error fetching profile for %s: %v — skipping participant", rp.Name, err)
			continue
		}

		if profile == nil {
			log.Printf("[collector] %s is not female — skipping participant", rp.Name)
			continue
		}

		// Create wrestler in DB
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
			ELO:            services.StartingELO,
		}

		for _, alias := range profile.AlterEgos {
			newWrestler.Aliases = append(newWrestler.Aliases, models.WrestlerAlias{
				Alias: alias,
			})
		}

		for name, url := range profile.Socials {
			newWrestler.Socials = append(newWrestler.Socials, models.Socials{
				Name: name,
				URL:  url,
			})
		}

		if err := p.db.Create(&newWrestler).Error; err != nil {
			log.Printf("[collector] Error creating wrestler %s: %v — skipping participant", profile.Name, err)
			continue
		}

		log.Printf("[collector] Created wrestler: %s (ELO: %.0f)", newWrestler.Name, services.StartingELO)
		wrestlers[rp.CagematchID] = newWrestler
	}

	// Only skip the match if we have zero resolved female participants
	if len(wrestlers) == 0 {
		return nil, true
	}

	return wrestlers, false
}
