package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// live.go — the "living data" layer over the all-time tables:
// 12-month performance ratings (chess-style tournament performance), 7-day
// rank movement for both orderings, and the on-this-day archive rotation.

const (
	perfWindowMonths = 12
	perfMinMatches   = 10
	moveLookbackDays = 7
)

type perfRow struct {
	Wid    uint
	N      int
	W      int
	D      int
	AvgOpp float64
	Score  float64
}

// perfRatings computes a performance rating per wrestler from matches in
// [from, to): the average opponent ELO at match time plus a linear score
// bonus, the way chess rates tournament performance. Unlike a windowed ELO
// delta this measures the *quality of recent results*, so an established
// wrestler holding her level scores high without needing to climb.
func perfRatings(db *gorm.DB, from, to string) (map[uint]perfRow, error) {
	var rows []perfRow
	err := db.Raw(`
		WITH recent AS (
			SELECT mp.wrestler_id AS wid, mp.match_id, mp.team,
			       CASE WHEN m.is_draw THEN 0.5 WHEN mp.is_winner THEN 1.0 ELSE 0.0 END AS pts,
			       CASE WHEN m.is_draw THEN 0 WHEN mp.is_winner THEN 1 ELSE 0 END AS win,
			       CASE WHEN m.is_draw THEN 1 ELSE 0 END AS draw
			FROM match_participants mp
			JOIN matches m ON m.id = mp.match_id
			WHERE mp.wrestler_id > 0 AND m.date >= ? AND m.date < ?
		),
		opp AS (
			SELECT r.wid, r.match_id, AVG(eh.elo) AS opp_elo
			FROM recent r
			JOIN match_participants o ON o.match_id = r.match_id
			     AND o.team != r.team AND o.wrestler_id > 0
			JOIN elo_histories eh ON eh.match_id = r.match_id AND eh.wrestler_id = o.wrestler_id
			GROUP BY r.wid, r.match_id
		)
		SELECT r.wid AS wid, COUNT(*) AS n, SUM(r.win) AS w, SUM(r.draw) AS d,
		       AVG(o.opp_elo) AS avg_opp, SUM(r.pts) AS score
		FROM recent r
		JOIN opp o ON o.wid = r.wid AND o.match_id = r.match_id
		GROUP BY r.wid
		HAVING COUNT(*) >= ?
	`, from, to, perfMinMatches).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]perfRow, len(rows))
	for _, r := range rows {
		out[r.Wid] = r
	}
	return out, nil
}

func perfValue(r perfRow) float64 {
	return r.AvgOpp + 400*(2*r.Score/float64(r.N)-1)
}

// rankOf turns id→value into id→rank (1 = highest value).
func rankOf(vals map[uint]float64) map[uint]int {
	type kv struct {
		id uint
		v  float64
	}
	list := make([]kv, 0, len(vals))
	for id, v := range vals {
		list = append(list, kv{id, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	ranks := make(map[uint]int, len(list))
	for i, e := range list {
		ranks[e.id] = i + 1
	}
	return ranks
}

// eloRanksAt reconstructs every wrestler's ELO as of a past instant from
// elo_histories and ranks them.
func eloRanksAt(db *gorm.DB, cutoff string) (map[uint]int, error) {
	var rows []struct {
		Wid uint
		Elo float64
	}
	err := db.Raw(`
		SELECT wrestler_id AS wid, elo FROM (
			SELECT wrestler_id, elo,
			       ROW_NUMBER() OVER (PARTITION BY wrestler_id ORDER BY match_date DESC, id DESC) AS rn
			FROM elo_histories WHERE wrestler_id > 0 AND match_date < ?
		) WHERE rn = 1
	`, cutoff).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	vals := make(map[uint]float64, len(rows))
	for _, r := range rows {
		vals[r.Wid] = r.Elo
	}
	return rankOf(vals), nil
}

// GET /api/rankings-extras
func GetRankingsExtras(db *gorm.DB) gin.HandlerFunc {
	type extra struct {
		Perf     *float64 `json:"perf,omitempty"`
		N        int      `json:"n,omitempty"`
		W        int      `json:"w,omitempty"`
		L        int      `json:"l,omitempty"`
		D        int      `json:"d,omitempty"`
		Move     *int     `json:"move"`
		PerfMove *int     `json:"perf_move"`
	}

	type cacheEntry struct {
		data []byte
		at   time.Time
	}
	caches := map[int]*cacheEntry{}
	var mu sync.Mutex
	const ttl = 30 * time.Minute

	return func(c *gin.Context) {
		months := 12
		switch c.Query("months") {
		case "6":
			months = 6
		case "24":
			months = 24
		case "60":
			months = 60
		}

		mu.Lock()
		if e, ok := caches[months]; ok && time.Since(e.at) < ttl {
			data := e.data
			mu.Unlock()
			c.Header("Cache-Control", "public, max-age=600")
			c.Data(http.StatusOK, "application/json; charset=utf-8", data)
			return
		}
		mu.Unlock()

		now := time.Now()
		day := func(t time.Time) string { return t.Format("2006-01-02") }
		weekAgo := now.AddDate(0, 0, -moveLookbackDays)

		// All-time ELO per wrestler — both the all-time ranking and the anchor
		// for the form blend below.
		var cur []struct {
			ID  uint
			Elo float64
		}
		if err := db.Raw(`SELECT id, elo FROM wrestlers`).Scan(&cur).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rank query failed"})
			return
		}
		curVals := make(map[uint]float64, len(cur))
		for _, r := range cur {
			curVals[r.ID] = r.Elo
		}
		curRanks := rankOf(curVals)

		// Form ratings: current window and the window as it stood a week ago.
		perfNow, err := perfRatings(db, day(now.AddDate(0, -months, 0)), day(now.AddDate(0, 0, 1)))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "perf query failed"})
			return
		}
		perfPast, err := perfRatings(db, day(weekAgo.AddDate(0, -months, 0)), day(weekAgo))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "perf query failed"})
			return
		}

		// Anchored form: blend the window's performance rating 50/50 with the
		// wrestler's all-time ELO. Raw TPR is capped by opponent strength, so
		// siloed rosters that only face each other (WWE vs joshi) can't be
		// compared on it alone; the career anchor keeps cross-pool ordering
		// sane while recent results still move the number.
		blend := func(id uint, r perfRow) float64 {
			v := perfValue(r)
			if elo, ok := curVals[id]; ok {
				return 0.5*v + 0.5*elo
			}
			return v
		}
		perfNowVals := make(map[uint]float64, len(perfNow))
		for id, r := range perfNow {
			perfNowVals[id] = blend(id, r)
		}
		perfPastVals := make(map[uint]float64, len(perfPast))
		for id, r := range perfPast {
			perfPastVals[id] = blend(id, r)
		}
		perfRanksNow := rankOf(perfNowVals)
		perfRanksPast := rankOf(perfPastVals)
		pastRanks, err := eloRanksAt(db, day(weekAgo))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rank history query failed"})
			return
		}

		extras := make(map[uint]*extra, len(cur))
		get := func(id uint) *extra {
			if e, ok := extras[id]; ok {
				return e
			}
			e := &extra{}
			extras[id] = e
			return e
		}
		for id, r := range perfNow {
			v := perfNowVals[id]
			e := get(id)
			e.Perf = &v
			e.N, e.W, e.D = r.N, r.W, r.D
			e.L = r.N - r.W - r.D
			if pr, ok := perfRanksPast[id]; ok {
				mv := pr - perfRanksNow[id]
				e.PerfMove = &mv
			}
		}
		for id, cr := range curRanks {
			if pr, ok := pastRanks[id]; ok {
				mv := pr - cr
				get(id).Move = &mv
			}
		}

		resp := gin.H{
			"window_months": months,
			"min_matches":   perfMinMatches,
			"lookback_days": moveLookbackDays,
			"extras":        extras,
		}
		c.JSON(http.StatusOK, resp)

		if data, err := json.Marshal(resp); err == nil {
			mu.Lock()
			caches[months] = &cacheEntry{data: data, at: time.Now()}
			mu.Unlock()
		}
	}
}

// GET /api/peak-elos — every wrestler's all-time peak ELO and when it was
// set, for the stats page's record boards (current ELO understates retired
// legends). One group-by over elo_histories, cached 30 min.
func GetPeakElos(db *gorm.DB) gin.HandlerFunc {
	type peak struct {
		Peak float64 `json:"peak"`
		Date string  `json:"date"`
	}

	var cached []byte
	var cachedAt time.Time
	var mu sync.Mutex
	const ttl = 30 * time.Minute

	return func(c *gin.Context) {
		mu.Lock()
		if cached != nil && time.Since(cachedAt) < ttl {
			data := cached
			mu.Unlock()
			c.Header("Cache-Control", "public, max-age=600")
			c.Data(http.StatusOK, "application/json; charset=utf-8", data)
			return
		}
		mu.Unlock()

		var rows []struct {
			Wid  uint
			Peak float64
			Date string
		}
		err := db.Raw(`
			SELECT wrestler_id AS wid, elo AS peak, substr(match_date, 1, 10) AS date FROM (
				SELECT wrestler_id, elo, match_date,
				       ROW_NUMBER() OVER (PARTITION BY wrestler_id ORDER BY elo DESC, match_date ASC) AS rn
				FROM elo_histories WHERE wrestler_id > 0
			) WHERE rn = 1
		`).Scan(&rows).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
			return
		}
		peaks := make(map[uint]peak, len(rows))
		for _, r := range rows {
			peaks[r.Wid] = peak{Peak: r.Peak, Date: r.Date}
		}

		resp := gin.H{"peaks": peaks}
		c.JSON(http.StatusOK, resp)

		if data, err := json.Marshal(resp); err == nil {
			mu.Lock()
			cached = data
			cachedAt = time.Now()
			mu.Unlock()
		}
	}
}

// GET /api/on-this-day — notable matches from past years on today's date.
func GetOnThisDay(db *gorm.DB) gin.HandlerFunc {
	type participant struct {
		ID   *uint  `json:"id"`
		Name string `json:"name"`
	}
	type item struct {
		Year         string        `json:"year"`
		EventName    string        `json:"event_name"`
		CmEventID    *int          `json:"cagematch_event_id"`
		IsTitleMatch bool          `json:"is_title_match"`
		IsDraw       bool          `json:"is_draw"`
		Winners      []participant `json:"winners"`
		Losers       []participant `json:"losers"`
	}

	var cached []byte
	var cachedDay string
	var mu sync.Mutex

	return func(c *gin.Context) {
		today := time.Now().Format("01-02")
		mu.Lock()
		if cached != nil && cachedDay == today {
			data := cached
			mu.Unlock()
			c.Header("Cache-Control", "public, max-age=3600")
			c.Data(http.StatusOK, "application/json; charset=utf-8", data)
			return
		}
		mu.Unlock()

		// Rank today's historic matches by star power: combined current ELO of
		// everyone in the ring, with a bump for title matches.
		var rows []struct {
			ID        uint
			EventName string
			Year      string
			IsTitle   bool
			IsDraw    bool
			CmEventID *int
		}
		err := db.Raw(`
			SELECT m.id AS id, m.event_name AS event_name, substr(m.date, 1, 4) AS year,
			       m.is_title_match AS is_title, m.is_draw AS is_draw,
			       m.cagematch_event_id AS cm_event_id
			FROM matches m
			JOIN match_participants mp ON mp.match_id = m.id
			JOIN wrestlers w ON w.id = mp.wrestler_id
			WHERE substr(m.date, 6, 5) = ? AND substr(m.date, 1, 4) < ?
			GROUP BY m.id
			ORDER BY (CASE WHEN m.is_title_match THEN 3000.0 ELSE 0.0 END) + SUM(w.elo) DESC
			LIMIT 60
		`, today, time.Now().Format("2006")).Scan(&rows).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
			return
		}

		// At most one match per year, best-first, capped at 8 — then shown
		// chronologically.
		seenYear := map[string]bool{}
		var picked []struct {
			ID        uint
			EventName string
			Year      string
			IsTitle   bool
			IsDraw    bool
			CmEventID *int
		}
		for _, r := range rows {
			if seenYear[r.Year] {
				continue
			}
			seenYear[r.Year] = true
			picked = append(picked, r)
			if len(picked) >= 8 {
				break
			}
		}

		items := []item{}
		if len(picked) > 0 {
			ids := make([]uint, len(picked))
			for i, p := range picked {
				ids[i] = p.ID
			}
			var parts []struct {
				MatchID  uint
				WID      *uint
				IsWinner bool
				Name     string
			}
			if err := db.Raw(`
				SELECT mp.match_id AS match_id, mp.wrestler_id AS w_id, mp.is_winner AS is_winner,
				       COALESCE(w.name, mp.ghost_name, 'Unknown') AS name
				FROM match_participants mp
				LEFT JOIN wrestlers w ON w.id = mp.wrestler_id
				WHERE mp.match_id IN ?
			`, ids).Scan(&parts).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
				return
			}
			byMatch := map[uint][]struct {
				MatchID  uint
				WID      *uint
				IsWinner bool
				Name     string
			}{}
			for _, p := range parts {
				byMatch[p.MatchID] = append(byMatch[p.MatchID], p)
			}
			for _, m := range picked {
				it := item{
					Year: m.Year, EventName: m.EventName, CmEventID: m.CmEventID,
					IsTitleMatch: m.IsTitle, IsDraw: m.IsDraw,
					Winners: []participant{}, Losers: []participant{},
				}
				for _, p := range byMatch[m.ID] {
					pp := participant{ID: p.WID, Name: p.Name}
					if p.IsWinner {
						it.Winners = append(it.Winners, pp)
					} else {
						it.Losers = append(it.Losers, pp)
					}
				}
				items = append(items, it)
			}
			sort.Slice(items, func(i, j int) bool { return items[i].Year < items[j].Year })
		}

		resp := gin.H{"date": today, "items": items}
		c.JSON(http.StatusOK, resp)

		if data, err := json.Marshal(resp); err == nil {
			mu.Lock()
			cached = data
			cachedDay = today
			mu.Unlock()
		}
	}
}
