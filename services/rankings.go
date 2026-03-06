package services

import (
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

func BuildRankings(db *gorm.DB, wrestlers []models.Wrestler) []models.Ranking {
	total := len(wrestlers)
	var rankings []models.Ranking
	for i, w := range wrestlers {
		trends := calculateTrends(db, w.ID)
		tier, icon := getTier(i+1, total)
		rankings = append(rankings, models.Ranking{
			Rank:     i + 1,
			Tier:     tier,
			TierIcon: icon,
			Wrestler: w,
			Trends:   trends,
		})
	}
	return rankings
}

// getTier returns the SC2-style tier based on percentile rank.
// Wrestlers are already sorted by ELO descending, so rank 1 = best.
//
// TierDef defines a single rank tier. Exported so the API can serve it.
type TierDef struct {
	MaxPct float64 `json:"max_pct"`
	Name   string  `json:"name"`
	Icon   string  `json:"icon"`
	Color  string  `json:"color"`
}

// Tiers is the single source of truth for rank definitions.
// Ordered from top (most elite) to bottom (catch-all).
var Tiers = []TierDef{
	{MaxPct: 0.5, Name: "Jotei 2", Icon: "👑", Color: "#ffd700"},
	{MaxPct: 1.0, Name: "Jotei 1", Icon: "👑", Color: "#ffd700"},
	{MaxPct: 2.0, Name: "Ace 3", Icon: "⚡", Color: "#ffa500"},
	{MaxPct: 3.5, Name: "Ace 2", Icon: "⚡", Color: "#ffa500"},
	{MaxPct: 5.0, Name: "Ace 1", Icon: "⚡", Color: "#ffa500"},
	{MaxPct: 7.5, Name: "Senshi 3", Icon: "🔥", Color: "#e91e63"},
	{MaxPct: 10.0, Name: "Senshi 2", Icon: "🔥", Color: "#e91e63"},
	{MaxPct: 12.5, Name: "Senshi 1", Icon: "🔥", Color: "#e91e63"},
	{MaxPct: 16.5, Name: "Estrella 3", Icon: "⭐", Color: "#64b5f6"},
	{MaxPct: 22.5, Name: "Estrella 2", Icon: "⭐", Color: "#64b5f6"},
	{MaxPct: 30.0, Name: "Estrella 1", Icon: "⭐", Color: "#64b5f6"},
	{MaxPct: 40.0, Name: "Young Lioness", Icon: "🌙", Color: "#81c784"},
	{MaxPct: 100.0, Name: "Seedling", Icon: "🌱", Color: "#888"},
}

func getTier(rank, total int) (string, string) {
	if total == 0 {
		return "Seadling", "🌱"
	}
	pct := float64(rank) / float64(total) * 100

	for _, t := range Tiers {
		if pct <= t.MaxPct {
			return t.Name, t.Icon
		}
	}
	return "Seadling", "🌱"
}

func calculateTrends(db *gorm.DB, wrestlerID uint) models.Trends {
	var history []models.ELOHistory
	db.Where("wrestler_id = ?", wrestlerID).Order("created_at DESC").Limit(10).Find(&history)
	return models.Trends{
		Last2:  getTrend(history, 2),
		Last5:  getTrend(history, 5),
		Last10: getTrend(history, 10),
		Streak: getStreak(db, wrestlerID),
	}
}

func getTrend(history []models.ELOHistory, n int) string {
	if len(history) < n {
		return "stable"
	}

	latest := history[0].ELO
	oldest := history[n-1].ELO
	diff := latest - oldest

	if diff > 10 {
		return "up"
	} else if diff < -10 {
		return "down"
	}
	return "stable"
}

func getStreak(db *gorm.DB, wrestlerID uint) int {
	var participants []models.MatchParticipant
	db.Where("wrestler_id = ?", wrestlerID).Order("created_at DESC").Limit(20).Find(&participants)
	if len(participants) == 0 {
		return 0
	}

	streak := 0
	firstResult := participants[0].IsWinner

	for _, p := range participants {
		if p.IsWinner == firstResult {
			if firstResult {
				streak++
			} else {
				streak--
			}
		} else {
			break
		}
	}
	return streak
}
