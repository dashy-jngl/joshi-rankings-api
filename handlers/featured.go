package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Featured response types
type FeaturedWrestler struct {
	WrestlerID uint    `json:"wrestler_id"`
	Name       string  `json:"name"`
	Promotion  string  `json:"promotion"`
	ELO        float64 `json:"elo"`
	ELOGain    float64 `json:"elo_gain"`
	Wins       int     `json:"wins"`
	Losses     int     `json:"losses"`
	Image      string  `json:"image"`
}

type TitleChange struct {
	TitleName        string `json:"title_name"`
	CagematchTitleID int    `json:"cagematch_title_id"`
	NewChampion      string `json:"new_champion"`
	WrestlerID       uint   `json:"wrestler_id"`
	WonDate          string `json:"won_date"`
	Promotion        string `json:"promotion"`
	CagematchEventID int    `json:"cagematch_event_id"`
}

type Upset struct {
	EventName       string  `json:"event_name"`
	Date            string  `json:"date"`
	MatchID         uint    `json:"match_id"`
	CagematchEventID int    `json:"cagematch_event_id"`
	WinnerName string `json:"winner_name"`
	WinnerID   uint   `json:"winner_id"`
	WinnerELO  float64 `json:"winner_elo"`
	LoserName  string  `json:"loser_name"`
	LoserID    uint    `json:"loser_id"`
	LoserELO   float64 `json:"loser_elo"`
	ELOGap     float64 `json:"elo_gap"`
}

type FeaturedStreak struct {
	WrestlerID uint    `json:"wrestler_id"`
	Name       string  `json:"name"`
	Promotion  string  `json:"promotion"`
	Streak     int     `json:"streak"`
	ELO        float64 `json:"elo"`
}

type RisingStar struct {
	WrestlerID  uint    `json:"wrestler_id"`
	Name        string  `json:"name"`
	Promotion   string  `json:"promotion"`
	ELO         float64 `json:"elo"`
	ELOGain30d  float64 `json:"elo_gain_30d"`
	StartELO    float64 `json:"start_elo"`
}

type MatchParticipantInfo struct {
	Name              string  `json:"name"`
	WrestlerID        uint    `json:"wrestler_id"`
	GhostCagematchID  int     `json:"ghost_cagematch_id"`
	Team              int     `json:"team"`
	IsWinner          bool    `json:"is_winner"`
	ELOChange         float64 `json:"elo_change"`
}

type RecentMatch struct {
	EventName        string                 `json:"event_name"`
	Date             string                 `json:"date"`
	MatchType        string                 `json:"match_type"`
	CagematchEventID int                    `json:"cagematch_event_id"`
	Participants     []MatchParticipantInfo `json:"participants"`
}

type PowerShift struct {
	Promotion string  `json:"promotion"`
	ELOChange float64 `json:"elo_change"`
	Direction string  `json:"direction"`
}

type FeaturedResponse struct {
	WrestlerOfWeek     *FeaturedWrestler  `json:"wrestler_of_week"`
	WrestlerOfMonth    *FeaturedWrestler  `json:"wrestler_of_month"`
	WrestlerOfYear     *FeaturedWrestler  `json:"wrestler_of_year"`
	TopWeek            []FeaturedWrestler `json:"top_week"`
	TopMonth           []FeaturedWrestler `json:"top_month"`
	TopYear            []FeaturedWrestler `json:"top_year"`
	RecentTitleChanges []TitleChange     `json:"recent_title_changes"`
	BiggestUpsets      []Upset           `json:"biggest_upsets"`
	HotStreaks         []FeaturedStreak     `json:"hot_streaks"`
	ColdStreaks        []FeaturedStreak     `json:"cold_streaks"`
	RisingStars        []RisingStar      `json:"rising_stars"`
	RecentMatches      []RecentMatch     `json:"recent_matches"`
	PowerShift         []PowerShift      `json:"power_shift"`
}

func GetFeatured(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := FeaturedResponse{}

		now := time.Now().UTC()

		// Wrestler of Week (last 7 days)
		weekStart := now.AddDate(0, 0, -7)
		topWeek := getTopGainers(db, weekStart, now, 5)
		if len(topWeek) > 0 {
			resp.WrestlerOfWeek = &topWeek[0]
		}
		resp.TopWeek = topWeek

		// Wrestler of Month (last 30 days)
		monthStart := now.AddDate(0, 0, -30)
		topMonth := getTopGainers(db, monthStart, now, 5)
		if len(topMonth) > 0 {
			resp.WrestlerOfMonth = &topMonth[0]
		}
		resp.TopMonth = topMonth

		// Wrestler of Year (last 365 days)
		yearStart := now.AddDate(0, 0, -365)
		topYear := getTopGainers(db, yearStart, now, 5)
		if len(topYear) > 0 {
			resp.WrestlerOfYear = &topYear[0]
		}
		resp.TopYear = topYear

		// Recent title changes
		resp.RecentTitleChanges = getRecentTitleChanges(db)

		// Biggest upsets (last 30 days)
		resp.BiggestUpsets = getBiggestUpsets(db, now.AddDate(0, 0, -30))

		// Streaks
		resp.HotStreaks, resp.ColdStreaks = getStreaks(db)

		// Rising stars
		resp.RisingStars = getRisingStars(db, now.AddDate(0, 0, -30))

		// Recent matches
		resp.RecentMatches = getRecentMatches(db)

		// Power shift
		resp.PowerShift = getPowerShift(db, now.AddDate(0, 0, -30))

		c.JSON(http.StatusOK, resp)
	}
}

func getTopGainers(db *gorm.DB, start, end time.Time, limit int) []FeaturedWrestler {
	type result struct {
		WrestlerID uint
		Name       string
		Promotion  string
		ELO        float64
		Image      string
		StartELO   float64
		EndELO     float64
	}

	var rows []result
	db.Raw(`
		SELECT mp.wrestler_id,
			w.name, w.promotion, w.elo, w.image_url as image,
			w.elo - SUM(mp.elo_change) as start_elo,
			w.elo as end_elo
		FROM match_participants mp
		JOIN matches m ON m.id = mp.match_id
		JOIN wrestlers w ON w.id = mp.wrestler_id
		WHERE m.date >= ? AND m.date <= ?
		AND mp.wrestler_id > 0
		GROUP BY mp.wrestler_id
		HAVING SUM(mp.elo_change) > 0
		ORDER BY SUM(mp.elo_change) DESC
		LIMIT ?
	`, start, end, limit).Scan(&rows)

	if len(rows) == 0 {
		return []FeaturedWrestler{}
	}

	out := make([]FeaturedWrestler, 0, len(rows))
	for _, r := range rows {
		gain := r.EndELO - r.StartELO
		var wins, losses int
		db.Raw(`
			SELECT 
				SUM(CASE WHEN mp.is_winner = 1 THEN 1 ELSE 0 END) as wins,
				SUM(CASE WHEN mp.is_winner = 0 THEN 1 ELSE 0 END) as losses
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			WHERE mp.wrestler_id = ? AND m.date >= ? AND m.date <= ?
		`, r.WrestlerID, start, end).Row().Scan(&wins, &losses)

		out = append(out, FeaturedWrestler{
			WrestlerID: r.WrestlerID,
			Name:       r.Name,
			Promotion:  r.Promotion,
			ELO:        r.ELO,
			ELOGain:    gain,
			Wins:       wins,
			Losses:     losses,
			Image:      r.Image,
		})
	}
	return out
}

func getRecentTitleChanges(db *gorm.DB) []TitleChange {
	var rows []TitleChange
	db.Raw(`
		SELECT tr.title_name, tr.cagematch_title_id,
			GROUP_CONCAT(COALESCE(w.name, tr.holder_name), ' & ') as new_champion,
			MIN(tr.wrestler_id) as wrestler_id, tr.won_date,
			COALESCE(MIN(w.promotion), '') as promotion,
			COALESCE((
				SELECT m.cagematch_event_id FROM matches m
				JOIN match_participants mp ON mp.match_id = m.id
				WHERE m.is_title_match = 1
				AND date(m.date) = date(tr.won_date)
				AND mp.wrestler_id = tr.wrestler_id
				AND mp.is_winner = 1
				AND m.cagematch_event_id > 0
				LIMIT 1
			), 0) as cagematch_event_id
		FROM title_reigns tr
		LEFT JOIN wrestlers w ON w.id = tr.wrestler_id
		WHERE tr.won_date >= date('now', '-90 days')
		GROUP BY tr.cagematch_title_id, tr.won_date
		HAVING SUM(CASE WHEN tr.untracked = 0 OR tr.untracked IS NULL THEN 1 ELSE 0 END) > 0
		ORDER BY tr.won_date DESC
		LIMIT 10
	`).Scan(&rows)
	if rows == nil {
		rows = []TitleChange{}
	}
	return rows
}

func getBiggestUpsets(db *gorm.DB, since time.Time) []Upset {
	var rows []Upset
	db.Raw(`
		SELECT m.event_name, m.date, m.id as match_id, COALESCE(m.cagematch_event_id, 0) as cagematch_event_id,
			w_win.name as winner_name, w_win.id as winner_id,
			(w_win.elo - mp_win.elo_change) as winner_elo,
			w_lose.name as loser_name, w_lose.id as loser_id,
			(w_lose.elo - mp_lose.elo_change) as loser_elo,
			((w_lose.elo - mp_lose.elo_change) - (w_win.elo - mp_win.elo_change)) as elo_gap
		FROM matches m
		JOIN match_participants mp_win ON mp_win.match_id = m.id AND mp_win.is_winner = 1
		JOIN match_participants mp_lose ON mp_lose.match_id = m.id AND mp_lose.is_winner = 0
		JOIN wrestlers w_win ON w_win.id = mp_win.wrestler_id
		JOIN wrestlers w_lose ON w_lose.id = mp_lose.wrestler_id
		WHERE m.date >= ?
		AND m.is_draw = 0
		AND mp_win.wrestler_id != 0 AND mp_lose.wrestler_id != 0
		AND ((w_lose.elo - mp_lose.elo_change) - (w_win.elo - mp_win.elo_change)) > 0
		AND (m.match_type IN ('singles', 'Singles') OR m.is_title_match = 1
		     OR (SELECT COUNT(*) FROM match_participants mp2 WHERE mp2.match_id = m.id) = 2)
		GROUP BY m.id
		ORDER BY elo_gap DESC
		LIMIT 10
	`, since).Scan(&rows)
	if rows == nil {
		rows = []Upset{}
	}
	return rows
}

func getStreaks(db *gorm.DB) (hot []FeaturedStreak, cold []FeaturedStreak) {
	// Get last 20 matches for active wrestlers, compute streaks in Go
	type matchResult struct {
		WrestlerID uint
		IsWinner   bool
		MatchDate  time.Time
	}

	var results []matchResult
	db.Raw(`
		SELECT mp.wrestler_id, mp.is_winner, m.date as match_date
		FROM match_participants mp
		JOIN matches m ON m.id = mp.match_id
		WHERE m.date >= date('now', '-60 days')
		AND mp.wrestler_id != 0
		AND m.is_draw = 0
		ORDER BY mp.wrestler_id, m.date DESC, m.id DESC
	`).Scan(&results)

	// Compute streaks per wrestler
	type streakInfo struct {
		wins   int
		losses int
	}
	streaks := make(map[uint]*streakInfo)

	var currentWrestler uint
	var currentStreak *streakInfo
	var streakBroken bool

	for _, r := range results {
		if r.WrestlerID != currentWrestler {
			currentWrestler = r.WrestlerID
			currentStreak = &streakInfo{}
			streaks[r.WrestlerID] = currentStreak
			streakBroken = false
		}
		if streakBroken {
			continue
		}
		if r.IsWinner {
			if currentStreak.losses > 0 {
				streakBroken = true
				continue
			}
			currentStreak.wins++
		} else {
			if currentStreak.wins > 0 {
				streakBroken = true
				continue
			}
			currentStreak.losses++
		}
	}

	// Collect wrestlers with streaks >= 3
	type wrestlerInfo struct {
		ID        uint
		Name      string
		Promotion string
		ELO       float64
	}

	var hotIDs, coldIDs []uint
	hotMap := make(map[uint]int)
	coldMap := make(map[uint]int)

	for wid, s := range streaks {
		if s.wins >= 3 {
			hotIDs = append(hotIDs, wid)
			hotMap[wid] = s.wins
		}
		if s.losses >= 3 {
			coldIDs = append(coldIDs, wid)
			coldMap[wid] = s.losses
		}
	}

	fetchStreakEntries := func(ids []uint, streakMap map[uint]int) []FeaturedStreak {
		if len(ids) == 0 {
			return []FeaturedStreak{}
		}
		var wrestlers []wrestlerInfo
		db.Raw("SELECT id, name, promotion, elo FROM wrestlers WHERE id IN ?", ids).Scan(&wrestlers)

		entries := make([]FeaturedStreak, 0, len(wrestlers))
		for _, w := range wrestlers {
			entries = append(entries, FeaturedStreak{
				WrestlerID: w.ID,
				Name:       w.Name,
				Promotion:  w.Promotion,
				Streak:     streakMap[w.ID],
				ELO:        w.ELO,
			})
		}
		// Sort by streak desc
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].Streak > entries[i].Streak {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
		if len(entries) > 5 {
			entries = entries[:5]
		}
		return entries
	}

	hot = fetchStreakEntries(hotIDs, hotMap)
	cold = fetchStreakEntries(coldIDs, coldMap)
	return
}

func getRisingStars(db *gorm.DB, since time.Time) []RisingStar {
	// Get average ELO
	var avgELO float64
	db.Raw("SELECT AVG(elo) FROM wrestlers WHERE match_count >= 10").Row().Scan(&avgELO)

	type risingRow struct {
		WrestlerID uint
		Name       string
		Promotion  string
		ELO        float64
		StartELO   float64
		EndELO     float64
	}
	var rawRows []risingRow
	db.Raw(`
		SELECT e_start.wrestler_id, w.name, w.promotion, w.elo,
			e_start.elo as start_elo, e_end.elo as end_elo
		FROM (
			SELECT wrestler_id, elo FROM elo_histories eh1
			WHERE eh1.match_date >= ?
			AND eh1.id = (SELECT MIN(eh2.id) FROM elo_histories eh2 WHERE eh2.wrestler_id = eh1.wrestler_id AND eh2.match_date >= ?)
		) e_start
		JOIN (
			SELECT wrestler_id, elo FROM elo_histories eh1
			WHERE eh1.match_date >= ?
			AND eh1.id = (SELECT MAX(eh2.id) FROM elo_histories eh2 WHERE eh2.wrestler_id = eh1.wrestler_id AND eh2.match_date >= ?)
		) e_end ON e_end.wrestler_id = e_start.wrestler_id
		JOIN wrestlers w ON w.id = e_start.wrestler_id
		WHERE e_start.elo < ?
		AND e_end.elo > e_start.elo
		ORDER BY (e_end.elo - e_start.elo) DESC
		LIMIT 5
	`, since, since, since, since, avgELO).Scan(&rawRows)

	var rows []RisingStar
	for _, r := range rawRows {
		rows = append(rows, RisingStar{
			WrestlerID: r.WrestlerID,
			Name:       r.Name,
			Promotion:  r.Promotion,
			ELO:        r.ELO,
			ELOGain30d: r.EndELO - r.StartELO,
			StartELO:   r.StartELO,
		})
	}
	if rows == nil {
		rows = []RisingStar{}
	}
	return rows
}

func getRecentMatches(db *gorm.DB) []RecentMatch {
	type matchRow struct {
		ID               uint
		EventName        string
		Date             time.Time
		MatchType        string
		CagematchEventID int
	}

	var matches []matchRow
	db.Raw(`
		SELECT id, event_name, date, match_type, COALESCE(cagematch_event_id, 0) as cagematch_event_id
		FROM matches
		ORDER BY date DESC, id DESC
		LIMIT 10
	`).Scan(&matches)

	result := make([]RecentMatch, 0, len(matches))
	for _, m := range matches {
		var participants []MatchParticipantInfo
		db.Raw(`
			SELECT COALESCE(w.name, mp.ghost_name) as name, mp.wrestler_id, COALESCE(mp.ghost_cagematch_id, 0) as ghost_cagematch_id, mp.team, mp.is_winner, mp.elo_change
			FROM match_participants mp
			LEFT JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE mp.match_id = ?
			ORDER BY mp.team, mp.id
		`, m.ID).Scan(&participants)

		result = append(result, RecentMatch{
			EventName:        m.EventName,
			Date:             m.Date.Format("2006-01-02"),
			MatchType:        m.MatchType,
			CagematchEventID: m.CagematchEventID,
			Participants:     participants,
		})
	}
	return result
}

func getPowerShift(db *gorm.DB, since time.Time) []PowerShift {
	var rows []PowerShift
	db.Raw(`
		SELECT w.promotion, ROUND(SUM(mp.elo_change), 1) as elo_change,
			CASE WHEN SUM(mp.elo_change) >= 0 THEN 'up' ELSE 'down' END as direction
		FROM match_participants mp
		JOIN matches m ON m.id = mp.match_id
		JOIN wrestlers w ON w.id = mp.wrestler_id
		WHERE m.date >= ?
		AND w.promotion IS NOT NULL AND w.promotion != ''
		GROUP BY w.promotion
		HAVING ABS(SUM(mp.elo_change)) > 0
		ORDER BY SUM(mp.elo_change) DESC
		LIMIT 10
	`, since).Scan(&rows)
	if rows == nil {
		rows = []PowerShift{}
	}
	return rows
}
