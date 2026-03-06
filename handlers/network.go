package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// NetworkNode represents a wrestler in the network
type NetworkNode struct {
	ID   uint    `json:"id"`
	Name string  `json:"name"`
	ELO  float64 `json:"elo"`
	Matches int  `json:"matches"`
	Promotion string `json:"promotion"`
	DebutYear int `json:"debut_year"`
}

// NetworkEdge represents a connection between two wrestlers
type NetworkEdge struct {
	Source      uint `json:"source"`
	Target      uint `json:"target"`
	MatchCount  int  `json:"match_count"`
	SourceWins  int  `json:"source_wins"`
	TargetWins  int  `json:"target_wins"`
	Draws       int  `json:"draws"`
}

// NetworkResponse contains nodes + edges for D3
type NetworkResponse struct {
	Nodes []NetworkNode `json:"nodes"`
	Edges []NetworkEdge `json:"edges"`
}

// GET /api/network/wrestler/:id?limit=30
// Returns a wrestler's opponent network
func GetWrestlerNetwork(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID"})
			return
		}
		limit := 30
		if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 4000 {
			limit = l
		}

		// Parse optional filters
		fromYear := 0
		toYear := 9999
		if f, err := strconv.Atoi(c.Query("from")); err == nil { fromYear = f }
		if t, err := strconv.Atoi(c.Query("to")); err == nil { toYear = t }
		promotion := c.Query("promotion")
		minMatches := 1
		if m, err := strconv.Atoi(c.Query("min_matches")); err == nil && m > 0 { minMatches = m }

		// Get top opponents by match count
		type OpponentRow struct {
			OpponentID uint
			MatchCount int
			Wins       int
			Losses     int
			Draws      int
		}
		var opponents []OpponentRow

		args := []interface{}{id, fromYear, toYear}
		promoFilter := ""
		if promotion != "" {
			promoFilter = "AND w_opp.promotion = ? "
			args = append(args, promotion)
		}
		args = append(args, minMatches, limit)

		query := `
			SELECT 
				op.wrestler_id as opponent_id,
				COUNT(DISTINCT op.match_id) as match_count,
				SUM(CASE WHEN me.is_winner = 1 AND op.is_winner = 0 THEN 1 ELSE 0 END) as wins,
				SUM(CASE WHEN me.is_winner = 0 AND op.is_winner = 1 THEN 1 ELSE 0 END) as losses,
				SUM(CASE WHEN me.is_winner = op.is_winner AND me.team = op.team THEN 1 ELSE 0 END) as draws
			FROM match_participants me
			JOIN match_participants op ON op.match_id = me.match_id AND op.wrestler_id != me.wrestler_id AND op.wrestler_id > 0
			JOIN matches m ON m.id = me.match_id
			LEFT JOIN wrestlers w_opp ON w_opp.id = op.wrestler_id
			WHERE me.wrestler_id = ?
			AND CAST(strftime('%Y', m.date) AS INTEGER) >= ?
			AND CAST(strftime('%Y', m.date) AS INTEGER) <= ?
			` + promoFilter + `
			GROUP BY op.wrestler_id
			HAVING match_count >= ?
			ORDER BY match_count DESC
			LIMIT ?
		`
		db.Raw(query, args...).Scan(&opponents)

		// Build node list — center wrestler + opponents
		var centerWrestler NetworkNode
		db.Raw("SELECT id, name, elo, (wins+losses+draws) as matches, promotion, debut_year FROM wrestlers WHERE id = ?", id).Scan(&centerWrestler)

		nodes := []NetworkNode{centerWrestler}
		edges := []NetworkEdge{}

		oppIDs := make([]uint, 0, len(opponents))
		for _, o := range opponents {
			oppIDs = append(oppIDs, o.OpponentID)
			edges = append(edges, NetworkEdge{
				Source:     uint(id),
				Target:     o.OpponentID,
				MatchCount: o.MatchCount,
				SourceWins: o.Wins,
				TargetWins: o.Losses,
				Draws:      o.Draws,
			})
		}

		if len(oppIDs) > 0 {
			// Batch fetch opponent nodes to avoid too many SQL variables
			var oppNodes []NetworkNode
			for i := 0; i < len(oppIDs); i += 400 {
				end := i + 400
				if end > len(oppIDs) { end = len(oppIDs) }
				var batch []NetworkNode
				db.Raw("SELECT id, name, elo, (wins+losses+draws) as matches, promotion, debut_year FROM wrestlers WHERE id IN ?", oppIDs[i:end]).Scan(&batch)
				oppNodes = append(oppNodes, batch...)
			}
			nodes = append(nodes, oppNodes...)

			// 2nd hop: get interconnections between opponents + their top connections
			// Only do this for manageable sizes to avoid SQLite variable limits
			type InterEdge struct {
				W1ID       uint `gorm:"column:w1_id"`
				W2ID       uint `gorm:"column:w2_id"`
				MatchCount int  `gorm:"column:match_count"`
				W1Wins     int  `gorm:"column:w1_wins"`
				W2Wins     int  `gorm:"column:w2_wins"`
				Draws      int  `gorm:"column:draws"`
			}
			var interEdges []InterEdge
			if len(oppIDs) <= 200 {
				db.Raw(`
					SELECT 
						p1.wrestler_id as w1_id, p2.wrestler_id as w2_id,
						COUNT(DISTINCT p1.match_id) as match_count,
						SUM(CASE WHEN p1.is_winner = 1 AND p2.is_winner = 0 THEN 1 ELSE 0 END) as w1_wins,
						SUM(CASE WHEN p2.is_winner = 1 AND p1.is_winner = 0 THEN 1 ELSE 0 END) as w2_wins,
						SUM(CASE WHEN p1.is_winner = p2.is_winner THEN 1 ELSE 0 END) as draws
					FROM match_participants p1
					JOIN match_participants p2 ON p2.match_id = p1.match_id 
						AND p2.wrestler_id > p1.wrestler_id
						AND p2.team != p1.team
					WHERE p1.wrestler_id IN ? AND p2.wrestler_id IN ?
					GROUP BY p1.wrestler_id, p2.wrestler_id
					HAVING match_count >= 3
				`, oppIDs, oppIDs).Scan(&interEdges)
			}

			for _, e := range interEdges {
				edges = append(edges, NetworkEdge{
					Source:     e.W1ID,
					Target:     e.W2ID,
					MatchCount: e.MatchCount,
					SourceWins: e.W1Wins,
					TargetWins: e.W2Wins,
					Draws:      e.Draws,
				})
			}

			// 2nd hop nodes: for each direct opponent, grab their top 3 connections
			// that aren't already in the graph
			nodeSet := make(map[uint]bool)
			nodeSet[uint(id)] = true
			for _, oid := range oppIDs {
				nodeSet[oid] = true
			}

			depth := 5 // top N connections per opponent to pull in
			if d, err := strconv.Atoi(c.Query("depth")); err == nil && d > 0 && d <= 20 {
				depth = d
			}

			type Hop2Row struct {
				SourceID   uint
				OpponentID uint
				MatchCount int
				Wins       int
				Losses     int
			}
			var hop2Rows []Hop2Row
			if len(oppIDs) <= 200 {
				excludeIDs := append([]uint{uint(id)}, oppIDs...)
				db.Raw(`
					SELECT 
						me.wrestler_id as source_id,
						op.wrestler_id as opponent_id,
						COUNT(DISTINCT op.match_id) as match_count,
						SUM(CASE WHEN me.is_winner = 1 AND op.is_winner = 0 THEN 1 ELSE 0 END) as wins,
						SUM(CASE WHEN me.is_winner = 0 AND op.is_winner = 1 THEN 1 ELSE 0 END) as losses
					FROM match_participants me
					JOIN match_participants op ON op.match_id = me.match_id AND op.wrestler_id != me.wrestler_id AND op.wrestler_id > 0
					WHERE me.wrestler_id IN ? AND op.wrestler_id NOT IN ?
					GROUP BY me.wrestler_id, op.wrestler_id
					HAVING match_count >= 5
					ORDER BY match_count DESC
				`, oppIDs, excludeIDs).Scan(&hop2Rows)
			}

			// Pick top N per source wrestler
			perSource := make(map[uint]int)
			var hop2IDs []uint
			hop2IDSet := make(map[uint]bool)
			for _, r := range hop2Rows {
				if perSource[r.SourceID] >= depth {
					continue
				}
				if hop2IDSet[r.OpponentID] {
					// Already adding this node, just add the edge
					edges = append(edges, NetworkEdge{
						Source: r.SourceID, Target: r.OpponentID,
						MatchCount: r.MatchCount, SourceWins: r.Wins, TargetWins: r.Losses,
					})
					perSource[r.SourceID]++
					continue
				}
				hop2IDs = append(hop2IDs, r.OpponentID)
				hop2IDSet[r.OpponentID] = true
				edges = append(edges, NetworkEdge{
					Source: r.SourceID, Target: r.OpponentID,
					MatchCount: r.MatchCount, SourceWins: r.Wins, TargetWins: r.Losses,
				})
				perSource[r.SourceID]++
			}

			if len(hop2IDs) > 0 {
				for i := 0; i < len(hop2IDs); i += 400 {
					end := i + 400
					if end > len(hop2IDs) { end = len(hop2IDs) }
					var batch []NetworkNode
					db.Raw("SELECT id, name, elo, (wins+losses+draws) as matches, promotion, debut_year FROM wrestlers WHERE id IN ?", hop2IDs[i:end]).Scan(&batch)
					nodes = append(nodes, batch...)
				}
			}
		}

		c.JSON(http.StatusOK, NetworkResponse{Nodes: nodes, Edges: edges})
	}
}

// GET /api/network/top?limit=50&min_matches=100&from=1990&to=2000
// Returns the most-connected wrestlers and their interconnections
func GetTopNetwork(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 50
		if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 4000 {
			limit = l
		}
		minMatches := 50
		if m, err := strconv.Atoi(c.Query("min_matches")); err == nil && m > 0 {
			minMatches = m
		}

		sortBy := c.DefaultQuery("sort", "matches")
		promotion := c.Query("promotion")
		fromYear := c.Query("from")
		toYear := c.Query("to")
		hasDateFilter := fromYear != "" && toYear != ""

		// Determine sort order
		orderClause := "COUNT(*) DESC"
		if sortBy == "elo" {
			orderClause = "MAX(w.elo) DESC"
		}

		// Get top wrestlers (optionally filtered by date range and/or promotion)
		var nodeIDs []uint
		if promotion != "" {
			// Promotion filter: get wrestlers FROM this promotion, then expand to include their opponents
			var promoWrestlerIDs []uint
			if hasDateFilter {
				db.Raw(`
					SELECT mp.wrestler_id as id
					FROM match_participants mp
					JOIN matches m ON m.id = mp.match_id
					JOIN wrestlers w ON w.id = mp.wrestler_id
					WHERE CAST(strftime('%Y', m.date) AS INTEGER) BETWEEN ? AND ?
					AND w.promotion = ?
					GROUP BY mp.wrestler_id HAVING COUNT(*) >= ?
					ORDER BY `+orderClause+` LIMIT ?`,
					fromYear, toYear, promotion, minMatches, limit).Scan(&promoWrestlerIDs)
			} else {
				q := `SELECT id FROM wrestlers WHERE (wins+losses+draws) >= ? AND promotion = ?`
				if sortBy == "elo" {
					q += ` ORDER BY elo DESC`
				} else {
					q += ` ORDER BY (wins+losses+draws) DESC`
				}
				q += ` LIMIT ?`
				db.Raw(q, minMatches, promotion, limit).Scan(&promoWrestlerIDs)
			}

			if len(promoWrestlerIDs) > 0 {
				// Find all opponents of these wrestlers
				var opponentIDs []uint
				db.Raw(`
					SELECT DISTINCT mp2.wrestler_id
					FROM match_participants mp1
					JOIN match_participants mp2 ON mp1.match_id = mp2.match_id AND mp1.wrestler_id != mp2.wrestler_id AND mp2.wrestler_id > 0
					WHERE mp1.wrestler_id IN ?`, promoWrestlerIDs).Scan(&opponentIDs)

				// Combine: promo wrestlers + their opponents (deduped)
				idSet := make(map[uint]bool)
				for _, id := range promoWrestlerIDs {
					idSet[id] = true
				}
				for _, id := range opponentIDs {
					idSet[id] = true
				}
				for id := range idSet {
					nodeIDs = append(nodeIDs, id)
				}
			}
		} else if hasDateFilter {
			db.Raw(`
				SELECT mp.wrestler_id as id
				FROM match_participants mp
				JOIN matches m ON m.id = mp.match_id
				JOIN wrestlers w ON w.id = mp.wrestler_id
				WHERE CAST(strftime('%Y', m.date) AS INTEGER) BETWEEN ? AND ?
				GROUP BY mp.wrestler_id HAVING COUNT(*) >= ?
				ORDER BY `+orderClause+` LIMIT ?`,
				fromYear, toYear, minMatches, limit).Scan(&nodeIDs)
		} else {
			q := `SELECT id FROM wrestlers WHERE (wins+losses+draws) >= ?`
			if sortBy == "elo" {
				q += ` ORDER BY elo DESC`
			} else {
				q += ` ORDER BY (wins+losses+draws) DESC`
			}
			q += ` LIMIT ?`
			db.Raw(q, minMatches, limit).Scan(&nodeIDs)
		}

		if len(nodeIDs) == 0 {
			c.JSON(http.StatusOK, NetworkResponse{Nodes: []NetworkNode{}, Edges: []NetworkEdge{}})
			return
		}

		// Get nodes
		var nodes []NetworkNode
		db.Raw("SELECT id, name, elo, (wins+losses+draws) as matches, promotion, debut_year FROM wrestlers WHERE id IN ?", nodeIDs).Scan(&nodes)

		// If date-filtered, override promotions with era-accurate ones from promotion_histories
		if hasDateFilter {
			for i, n := range nodes {
				var ph struct {
					Promotion string
				}
				// Get their primary promotion in that era (most matches)
				db.Raw(`
					SELECT promotion FROM promotion_histories
					WHERE wrestler_id = ? AND year BETWEEN ? AND ?
					GROUP BY promotion
					ORDER BY SUM(matches) DESC
					LIMIT 1
				`, n.ID, fromYear, toYear).Scan(&ph)
				if ph.Promotion != "" {
					nodes[i].Promotion = ph.Promotion
				}
			}
		}

		// Get edges between these wrestlers
		var edges []NetworkEdge
		edgeQuery := `
			SELECT 
				me.wrestler_id as source,
				op.wrestler_id as target,
				COUNT(DISTINCT me.match_id) as match_count,
				SUM(CASE WHEN me.is_winner = 1 AND op.is_winner = 0 THEN 1 ELSE 0 END) as source_wins,
				SUM(CASE WHEN me.is_winner = 0 AND op.is_winner = 1 THEN 1 ELSE 0 END) as target_wins,
				SUM(CASE WHEN me.is_winner = op.is_winner THEN 1 ELSE 0 END) as draws
			FROM match_participants me
			JOIN match_participants op ON op.match_id = me.match_id AND op.wrestler_id != me.wrestler_id AND op.wrestler_id > 0
		`
		// Use lower edge threshold for date-filtered views (fewer matches in narrow windows)
		edgeMin := 3
		if hasDateFilter {
			edgeMin = 1
		}

		if hasDateFilter {
			edgeQuery += ` JOIN matches m ON m.id = me.match_id
				WHERE CAST(strftime('%Y', m.date) AS INTEGER) BETWEEN ? AND ?
				AND me.wrestler_id IN ? AND op.wrestler_id IN ?
				AND me.wrestler_id < op.wrestler_id
				GROUP BY me.wrestler_id, op.wrestler_id
				HAVING match_count >= ?
				ORDER BY match_count DESC`
			db.Raw(edgeQuery, fromYear, toYear, nodeIDs, nodeIDs, edgeMin).Scan(&edges)
		} else {
			edgeQuery += ` WHERE me.wrestler_id IN ? AND op.wrestler_id IN ?
				AND me.wrestler_id < op.wrestler_id
				GROUP BY me.wrestler_id, op.wrestler_id
				HAVING match_count >= ?
				ORDER BY match_count DESC`
			db.Raw(edgeQuery, nodeIDs, nodeIDs, edgeMin).Scan(&edges)
		}

		c.JSON(http.StatusOK, NetworkResponse{Nodes: nodes, Edges: edges})
	}
}

// RivalryPair represents a rivalry between two wrestlers
type RivalryPair struct {
	Wrestler1ID   uint    `json:"wrestler1_id"`
	Wrestler1Name string  `json:"wrestler1_name"`
	Wrestler2ID   uint    `json:"wrestler2_id"`
	Wrestler2Name string  `json:"wrestler2_name"`
	MatchCount    int     `json:"match_count"`
	Singles       int     `json:"singles"`
	Tags          int     `json:"tags"`
	Trios         int     `json:"trios"`
	MultiPerson   int     `json:"multi_person"`
	W1Wins        int     `json:"w1_wins"`
	W2Wins        int     `json:"w2_wins"`
	Draws         int     `json:"draws"`
	FirstMatch    string  `json:"first_match"`
	LastMatch     string  `json:"last_match"`
	SpanYears     float64 `json:"span_years"`
}

// GET /api/network/rivalries?limit=50&min_matches=20
func GetRivalries(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := 50
		if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 200 {
			limit = l
		}
		minMatches := 20
		if m, err := strconv.Atoi(c.Query("min_matches")); err == nil && m > 0 {
			minMatches = m
		}

		var rivalries []RivalryPair

		// First get the pairs with counts, then enrich with match size breakdown
		// Two-step approach avoids extremely expensive single query
		type rawPair struct {
			Wrestler1ID   uint
			Wrestler1Name string
			Wrestler2ID   uint
			Wrestler2Name string
			MatchCount    int
			W1Wins        int
			W2Wins        int
			Draws         int
			FirstMatch    string
			LastMatch     string
			SpanYears     float64
		}
		var rawPairs []rawPair

		query := `
			SELECT 
				me.wrestler_id as wrestler1_id,
				w1.name as wrestler1_name,
				op.wrestler_id as wrestler2_id,
				w2.name as wrestler2_name,
				COUNT(DISTINCT me.match_id) as match_count,
				SUM(CASE WHEN me.is_winner = 1 AND op.is_winner = 0 THEN 1 ELSE 0 END) as w1_wins,
				SUM(CASE WHEN me.is_winner = 0 AND op.is_winner = 1 THEN 1 ELSE 0 END) as w2_wins,
				SUM(CASE WHEN me.is_winner = op.is_winner THEN 1 ELSE 0 END) as draws,
				MIN(m.date) as first_match,
				MAX(m.date) as last_match,
				ROUND((julianday(MAX(m.date)) - julianday(MIN(m.date))) / 365.25, 1) as span_years
			FROM match_participants me
			JOIN match_participants op ON op.match_id = me.match_id AND op.wrestler_id != me.wrestler_id AND op.wrestler_id > 0
			JOIN matches m ON m.id = me.match_id
			JOIN wrestlers w1 ON w1.id = me.wrestler_id
			JOIN wrestlers w2 ON w2.id = op.wrestler_id
			WHERE me.wrestler_id < op.wrestler_id
			AND me.team != op.team
			GROUP BY me.wrestler_id, op.wrestler_id
			HAVING match_count >= ?
			ORDER BY match_count DESC
			LIMIT ?
		`
		db.Raw(query, minMatches, limit).Scan(&rawPairs)

		// Now get match size breakdown for each pair
		for _, rp := range rawPairs {
			type sizeBreakdown struct {
				Singles     int
				Tags        int
				Trios       int
				MultiPerson int
			}
			var sb sizeBreakdown
			db.Raw(`
				SELECT
					SUM(CASE WHEN pcnt = 2 THEN 1 ELSE 0 END) as singles,
					SUM(CASE WHEN pcnt IN (3,4) THEN 1 ELSE 0 END) as tags,
					SUM(CASE WHEN pcnt IN (5,6) THEN 1 ELSE 0 END) as trios,
					SUM(CASE WHEN pcnt > 6 THEN 1 ELSE 0 END) as multi_person
				FROM (
					SELECT mp1.match_id, (SELECT COUNT(*) FROM match_participants mp WHERE mp.match_id = mp1.match_id) as pcnt
					FROM match_participants mp1
					JOIN match_participants mp2 ON mp2.match_id = mp1.match_id AND mp2.wrestler_id = ?
					WHERE mp1.wrestler_id = ? AND mp1.team != mp2.team
					GROUP BY mp1.match_id
				)
			`, rp.Wrestler2ID, rp.Wrestler1ID).Scan(&sb)

			rivalries = append(rivalries, RivalryPair{
				Wrestler1ID:   rp.Wrestler1ID,
				Wrestler1Name: rp.Wrestler1Name,
				Wrestler2ID:   rp.Wrestler2ID,
				Wrestler2Name: rp.Wrestler2Name,
				MatchCount:    rp.MatchCount,
				Singles:       sb.Singles,
				Tags:          sb.Tags,
				Trios:         sb.Trios,
				MultiPerson:   sb.MultiPerson,
				W1Wins:        rp.W1Wins,
				W2Wins:        rp.W2Wins,
				Draws:         rp.Draws,
				FirstMatch:    rp.FirstMatch,
				LastMatch:     rp.LastMatch,
				SpanYears:     rp.SpanYears,
			})
		}
		c.JSON(http.StatusOK, rivalries)
	}
}

// HeadToHeadPair contains pairwise stats between two wrestlers
type HeadToHeadPair struct {
	Wrestler1ID   uint   `json:"wrestler1_id"`
	Wrestler1Name string `json:"wrestler1_name"`
	Wrestler1ELO  int    `json:"wrestler1_elo"`
	Wrestler2ID   uint   `json:"wrestler2_id"`
	Wrestler2Name string `json:"wrestler2_name"`
	Wrestler2ELO  int    `json:"wrestler2_elo"`
	TotalMatches  int    `json:"total_matches"`
	AsOpponents   int    `json:"as_opponents"`
	AsPartners    int    `json:"as_partners"`
	Singles       int    `json:"singles"`
	Tags          int    `json:"tags"`
	MultiPerson   int    `json:"multi_person"`
	W1Wins        int    `json:"w1_wins"`
	W2Wins        int    `json:"w2_wins"`
	Draws         int    `json:"draws"`
	W1WinsSingles int    `json:"w1_wins_singles"`
	W2WinsSingles int    `json:"w2_wins_singles"`
	DrawsSingles  int    `json:"draws_singles"`
}

// HeadToHeadMatch is a shared match between selected wrestlers
type HeadToHeadMatch struct {
	MatchID          uint                   `json:"match_id"`
	Date             string                 `json:"date"`
	EventName        string                 `json:"event_name"`
	MatchType        string                 `json:"match_type"`
	IsDraw           bool                   `json:"is_draw"`
	IsTitleMatch     bool                   `json:"is_title_match"`
	MatchTime        string                 `json:"match_time"`
	FinishType       string                 `json:"finish_type"`
	ParticipantCount int                    `json:"participant_count"`
	MatchSize        string                 `json:"match_size"` // "singles", "tag", "multi"
	Relationship     string                 `json:"relationship"` // "opponents", "partners"
	Participants     []HeadToHeadParticipant `json:"participants"` // ALL participants in the match
}

// HeadToHeadParticipant is a wrestler's role in a shared match
type HeadToHeadParticipant struct {
	WrestlerID   uint   `json:"wrestler_id"`
	WrestlerName string `json:"wrestler_name"`
	Team         int    `json:"team"`
	IsWinner     bool   `json:"is_winner"`
	IsSelected   bool   `json:"is_selected"` // true if this wrestler is one of the compared ones
}

// HeadToHeadResponse contains pairwise stats and shared match history
type HeadToHeadResponse struct {
	Pairs   []HeadToHeadPair  `json:"pairs"`
	Matches []HeadToHeadMatch `json:"matches"`
}

// GET /api/network/head-to-head?ids=1,2,3
// Returns pairwise stats and shared match history for the given wrestler IDs
func GetHeadToHead(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		idsParam := c.Query("ids")
		if idsParam == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ids parameter required"})
			return
		}

		parts := strings.Split(idsParam, ",")
		if len(parts) < 2 || len(parts) > 10 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Provide 2-10 wrestler IDs"})
			return
		}

		var ids []uint
		for _, p := range parts {
			id, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wrestler ID: " + p})
				return
			}
			ids = append(ids, uint(id))
		}

		// Get wrestler info
		type wrestlerInfo struct {
			ID   uint
			Name string
			ELO  float64
		}
		var wrestlers []wrestlerInfo
		db.Raw("SELECT id, name, elo FROM wrestlers WHERE id IN ?", ids).Scan(&wrestlers)

		wMap := make(map[uint]wrestlerInfo)
		for _, w := range wrestlers {
			wMap[w.ID] = w
		}

		// Get pairwise stats for all combinations
		var pairs []HeadToHeadPair
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				id1, id2 := ids[i], ids[j]
				var pair struct {
					TotalMatches int
					W1Wins       int
					W2Wins       int
					Draws        int
				}
				db.Raw(`
					SELECT
						COUNT(DISTINCT mp1.match_id) as total_matches,
						SUM(CASE WHEN mp1.team != mp2.team AND mp1.is_winner = 1 AND mp2.is_winner = 0 THEN 1 ELSE 0 END) as w1_wins,
						SUM(CASE WHEN mp1.team != mp2.team AND mp1.is_winner = 0 AND mp2.is_winner = 1 THEN 1 ELSE 0 END) as w2_wins,
						SUM(CASE WHEN mp1.team != mp2.team AND mp1.is_winner = mp2.is_winner THEN 1 ELSE 0 END) as draws
					FROM match_participants mp1
					JOIN match_participants mp2 ON mp2.match_id = mp1.match_id AND mp2.wrestler_id = ?
					WHERE mp1.wrestler_id = ?
				`, id2, id1).Scan(&pair)

				// Count partner matches (same team)
				var partnerCount struct{ Count int }
				db.Raw(`
					SELECT COUNT(DISTINCT mp1.match_id) as count
					FROM match_participants mp1
					JOIN match_participants mp2 ON mp2.match_id = mp1.match_id AND mp2.wrestler_id = ?
					WHERE mp1.wrestler_id = ? AND mp1.team = mp2.team
				`, id2, id1).Scan(&partnerCount)

				// Match size breakdown (opponents only)
				var breakdown struct {
					Singles     int
					Tags        int
					MultiPerson int
				}
				db.Raw(`
					SELECT
						SUM(CASE WHEN pcnt = 2 THEN 1 ELSE 0 END) as singles,
						SUM(CASE WHEN pcnt IN (3,4) THEN 1 ELSE 0 END) as tags,
						SUM(CASE WHEN pcnt > 4 THEN 1 ELSE 0 END) as multi_person
					FROM (
						SELECT mp1.match_id, (SELECT COUNT(*) FROM match_participants mp WHERE mp.match_id = mp1.match_id) as pcnt
						FROM match_participants mp1
						JOIN match_participants mp2 ON mp2.match_id = mp1.match_id AND mp2.wrestler_id = ?
						WHERE mp1.wrestler_id = ? AND mp1.team != mp2.team
						GROUP BY mp1.match_id
					)
				`, id2, id1).Scan(&breakdown)

				// Singles-only win/loss/draw
				var singlesStats struct {
					W1Wins int
					W2Wins int
					Draws  int
				}
				db.Raw(`
					SELECT
						SUM(CASE WHEN mp1.is_winner = 1 AND mp2.is_winner = 0 THEN 1 ELSE 0 END) as w1_wins,
						SUM(CASE WHEN mp1.is_winner = 0 AND mp2.is_winner = 1 THEN 1 ELSE 0 END) as w2_wins,
						SUM(CASE WHEN mp1.is_winner = mp2.is_winner THEN 1 ELSE 0 END) as draws
					FROM match_participants mp1
					JOIN match_participants mp2 ON mp2.match_id = mp1.match_id AND mp2.wrestler_id = ?
					WHERE mp1.wrestler_id = ? AND mp1.team != mp2.team
					AND (SELECT COUNT(*) FROM match_participants mp WHERE mp.match_id = mp1.match_id) = 2
				`, id2, id1).Scan(&singlesStats)

				w1 := wMap[id1]
				w2 := wMap[id2]
				opponentMatches := pair.W1Wins + pair.W2Wins + pair.Draws
				pairs = append(pairs, HeadToHeadPair{
					Wrestler1ID:   id1,
					Wrestler1Name: w1.Name,
					Wrestler1ELO:  int(w1.ELO),
					Wrestler2ID:   id2,
					Wrestler2Name: w2.Name,
					Wrestler2ELO:  int(w2.ELO),
					TotalMatches:  pair.TotalMatches,
					AsOpponents:   opponentMatches,
					AsPartners:    partnerCount.Count,
					Singles:       breakdown.Singles,
					Tags:          breakdown.Tags,
					MultiPerson:   breakdown.MultiPerson,
					W1Wins:        pair.W1Wins,
					W2Wins:        pair.W2Wins,
					Draws:         pair.Draws,
					W1WinsSingles: singlesStats.W1Wins,
					W2WinsSingles: singlesStats.W2Wins,
					DrawsSingles:  singlesStats.Draws,
				})
			}
		}

		// Get shared matches — matches where at least 2 of the selected wrestlers participated
		type matchRow struct {
			MatchID          uint
			Date             string
			EventName        string
			MatchType        string
			IsDraw           bool
			IsTitleMatch     bool
			MatchTime        string
			FinishType       string
			ParticipantCount int
		}
		var matchRows []matchRow
		db.Raw(`
			SELECT m.id as match_id, m.date, m.event_name, m.match_type, m.is_draw, m.is_title_match,
				m.match_time, m.finish_type,
				(SELECT COUNT(*) FROM match_participants mp WHERE mp.match_id = m.id) as participant_count
			FROM matches m
			WHERE m.id IN (
				SELECT mp.match_id
				FROM match_participants mp
				WHERE mp.wrestler_id IN ?
				GROUP BY mp.match_id
				HAVING COUNT(DISTINCT mp.wrestler_id) >= 2
			)
			ORDER BY m.date DESC
			LIMIT 100
		`, ids).Scan(&matchRows)

		// Get participants for these matches (only the selected wrestlers)
		var matches []HeadToHeadMatch
		if len(matchRows) > 0 {
			matchIDs := make([]uint, len(matchRows))
			for i, mr := range matchRows {
				matchIDs[i] = mr.MatchID
			}

			// Get ALL participants for these matches (not just selected)
			type participantRow struct {
				MatchID      uint
				WrestlerID   uint
				WrestlerName string
				Team         int
				IsWinner     bool
			}
			var pRows []participantRow
			db.Raw(`
				SELECT mp.match_id, mp.wrestler_id, w.name as wrestler_name, mp.team, mp.is_winner
				FROM match_participants mp
				JOIN wrestlers w ON w.id = mp.wrestler_id
				WHERE mp.match_id IN ?
				ORDER BY mp.match_id, mp.team, mp.is_winner DESC
			`, matchIDs).Scan(&pRows)

			// Build selected ID set for marking
			selectedSet := make(map[uint]bool)
			for _, id := range ids {
				selectedSet[id] = true
			}

			// Group participants by match
			pMap := make(map[uint][]HeadToHeadParticipant)
			for _, pr := range pRows {
				pMap[pr.MatchID] = append(pMap[pr.MatchID], HeadToHeadParticipant{
					WrestlerID:   pr.WrestlerID,
					WrestlerName: pr.WrestlerName,
					Team:         pr.Team,
					IsWinner:     pr.IsWinner,
					IsSelected:   selectedSet[pr.WrestlerID],
				})
			}

			for _, mr := range matchRows {
				// Determine match size
				matchSize := "multi"
				if mr.ParticipantCount == 2 {
					matchSize = "singles"
				} else if mr.ParticipantCount <= 4 {
					matchSize = "tag"
				}

				// Determine relationship — are the selected wrestlers on same or different teams?
				relationship := "opponents"
				participants := pMap[mr.MatchID]
				selectedTeams := make(map[int]bool)
				for _, p := range participants {
					if selectedSet[p.WrestlerID] {
						selectedTeams[p.Team] = true
					}
				}
				if len(selectedTeams) == 1 {
					relationship = "partners"
				}

				matches = append(matches, HeadToHeadMatch{
					MatchID:          mr.MatchID,
					Date:             mr.Date,
					EventName:        mr.EventName,
					MatchType:        mr.MatchType,
					IsDraw:           mr.IsDraw,
					IsTitleMatch:     mr.IsTitleMatch,
					MatchTime:        mr.MatchTime,
					FinishType:       mr.FinishType,
					ParticipantCount: mr.ParticipantCount,
					MatchSize:        matchSize,
					Relationship:     relationship,
					Participants:     participants,
				})
			}
		}

		c.JSON(http.StatusOK, HeadToHeadResponse{
			Pairs:   pairs,
			Matches: matches,
		})
	}
}
