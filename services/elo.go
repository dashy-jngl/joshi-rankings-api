package services

import (
	"math"

	"joshi-rankings-api/models"
)

// ========================================
// TWEAKABLE VALUES — adjust these to tune the ELO system
// ========================================

const (
	// Starting ELO for new wrestlers
	StartingELO = 1000.0

	// ELO spread divisor — higher = wider spread between top and bottom
	// Standard chess uses 400, we use 800 for a wider range
	SpreadDivisor = 1600.0

	// Loss multiplier — how much a loss hurts relative to a win
	// 0.5 = losses hurt half as much as wins reward (wrestling is booked, not skill-only)
	LossMultiplier = 0.75

	// Title match multiplier — ELO change is multiplied by this for title matches
	TitleMultiplier = 1.5

	// Dynamic K-factors by ELO tier
	// Higher K = faster rating movement
	KTopTier   = 64.0           // 4000+ — stable
	KUpperCard = 2 * KTopTier   // 2500-4000 — moderate
	KMidcard   = 2.5 * KTopTier // 1500-2500 — solid movement
	KRookie    = 3 * KTopTier   // < 1500 — rookies move fast

	// K-factor tier thresholds
	ThresholdMidcard   = 1500.0
	ThresholdUpperCard = 2 * ThresholdMidcard
	ThresholdTopTier   = 3 * ThresholdMidcard
)

// Tiers (target ranges):
//   🌱 Rookie:       1000 (starting)
//   🌙 Young Lion:   1500-2000
//   ⭐ Midcard:      2000-3000
//   💎 Upper Card:   3000-4000
//   🔥 Main Eventer: 4000-5000
//   ⚡ Ace:          5000-5800
//   👑 Jotei:        5800-6500+

// ========================================
// TYPES
// ========================================

type ELOResult struct {
	WrestlerID uint    `json:"wrestler_id"`
	OldELO     float64 `json:"old_elo"`
	NewELO     float64 `json:"new_elo"`
	IsWinner   bool    `json:"is_winner"`
}

type RankingCalculator interface {
	CalculateMulti(winners, losers []models.Wrestler, IsTitleMatch, IsDraw bool) []ELOResult
}

type ELOCalculator struct{}

func NewELOCalculator() ELOCalculator {
	return ELOCalculator{}
}

// ========================================
// CALCULATION
// ========================================

// No ELO floor — let wrestlers sink naturally

func (e ELOCalculator) CalculateMulti(winners, losers []models.Wrestler, IsTitleMatch, IsDraw bool) []ELOResult {
	winnerAvg := averageELO(winners)
	loserAvg := averageELO(losers)

	// Title match bonus
	titleMult := 1.0
	if IsTitleMatch {
		titleMult = TitleMultiplier
	}

	// Tag team scaling — reduce K for larger teams
	teamSize := math.Max(float64(len(winners)), float64(len(losers)))
	teamScale := 1.0
	if teamSize > 1 {
		teamScale = 1 / math.Sqrt(teamSize)
	}

	// Expected score
	expected := 1 / (1 + math.Pow(10, (loserAvg-winnerAvg)/SpreadDivisor))

	var results []ELOResult

	for _, w := range winners {
		k := dynamicK(w) * titleMult * teamScale
		var change float64
		if IsDraw {
			drawChange := k * (0.5 - expected)
			lossChange := -k * (1 - expected) * LossMultiplier
			if drawChange < lossChange {
				drawChange = lossChange
			}
			change = drawChange
		} else {
			change = k * (1 - expected)
		}
		results = append(results, ELOResult{
			WrestlerID: w.ID,
			OldELO:     w.ELO,
			NewELO:     w.ELO + change,
			IsWinner:   true,
		})
	}

	for _, l := range losers {
		k := dynamicK(l) * titleMult * teamScale
		var change float64
		if IsDraw {
			drawChange := k * (0.5 - expected)
			lossChange := k * (1 - expected) * LossMultiplier
			if drawChange > lossChange {
				drawChange = lossChange
			}
			change = drawChange
		} else {
			change = k * (1 - expected) * LossMultiplier
		}
		newELO := l.ELO - change
		results = append(results, ELOResult{
			WrestlerID: l.ID,
			OldELO:     l.ELO,
			NewELO:     newELO,
			IsWinner:   false,
		})
	}

	return results
}

// ========================================
// HELPERS
// ========================================

// dynamicK returns a K-factor based on wrestler's current ELO.
func dynamicK(w models.Wrestler) float64 {
	switch {
	case w.ELO < ThresholdMidcard:
		return KRookie
	case w.ELO < ThresholdUpperCard:
		return KMidcard
	case w.ELO < ThresholdTopTier:
		return KUpperCard
	default:
		return KTopTier
	}
}

func averageELO(wrestlers []models.Wrestler) float64 {
	if len(wrestlers) == 0 {
		return StartingELO
	}
	total := 0.0
	for _, w := range wrestlers {
		total += w.ELO
	}
	return total / float64(len(wrestlers))
}
