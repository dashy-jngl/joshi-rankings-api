package handlers

import (
	"math"
	"math/rand"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"joshi-rankings-api/models"
)

type MatchPredictionRequest struct {
	Team1WrestlerIDs []uint `json:"team1_wrestler_ids"`
	Team2WrestlerIDs []uint `json:"team2_wrestler_ids"`
}

type MatchPredictionResponse struct {
	Team1Probability float64 `json:"team1_probability"`
	Team2Probability float64 `json:"team2_probability"`
	Team1AvgELO      float64 `json:"team1_avg_elo"`
	Team2AvgELO      float64 `json:"team2_avg_elo"`
	Team1AvgMomentum float64 `json:"team1_avg_momentum"`
	Team2AvgMomentum float64 `json:"team2_avg_momentum"`
	Team1Wrestlers   []WrestlerInfo `json:"team1_wrestlers"`
	Team2Wrestlers   []WrestlerInfo `json:"team2_wrestlers"`
}

type WrestlerInfo struct {
	ID       uint    `json:"id"`
	Name     string  `json:"name"`
	ELO      float64 `json:"elo"`
	Momentum float64 `json:"momentum"`
}

type TournamentRequest struct {
	Format        string `json:"format"`         // "single_elimination" or "round_robin"
	WrestlerIDs   []uint `json:"wrestler_ids"`
	BlockCount    int    `json:"block_count"`    // For round robin
	Deterministic bool   `json:"deterministic"`  // If true, favorite always wins
}

type TournamentResponse struct {
	Format  string      `json:"format"`
	Results interface{} `json:"results"`
}

type SingleElimBracket struct {
	Rounds [][]BracketMatch `json:"rounds"`
	Winner WrestlerInfo     `json:"winner"`
}

type BracketMatch struct {
	Wrestler1    *WrestlerInfo `json:"wrestler1"`
	Wrestler2    *WrestlerInfo `json:"wrestler2"`
	Winner       *WrestlerInfo `json:"winner"`
	Probability1 float64       `json:"probability1"`
	Probability2 float64       `json:"probability2"`
}

type RoundRobinResults struct {
	Blocks []RoundRobinBlock `json:"blocks"`
}

type RoundRobinBlock struct {
	BlockName string             `json:"block_name"`
	Standings []RoundRobinResult `json:"standings"`
	Matches   []RoundRobinMatch  `json:"matches"`
}

type RoundRobinResult struct {
	Wrestler WrestlerInfo `json:"wrestler"`
	Points   int          `json:"points"`
	Wins     int          `json:"wins"`
	Losses   int          `json:"losses"`
	Draws    int          `json:"draws"`
}

type RoundRobinMatch struct {
	Wrestler1    WrestlerInfo `json:"wrestler1"`
	Wrestler2    WrestlerInfo `json:"wrestler2"`
	Winner       *WrestlerInfo `json:"winner"` // nil for draw
	Probability1 float64       `json:"probability1"`
	Probability2 float64       `json:"probability2"`
	IsDraw       bool          `json:"is_draw"`
}

// POST /api/predict/match
func PredictMatch(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req MatchPredictionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
			return
		}

		if len(req.Team1WrestlerIDs) == 0 || len(req.Team2WrestlerIDs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Both teams must have at least one wrestler"})
			return
		}

		// Get wrestlers from database
		team1Wrestlers, err := getWrestlers(db, req.Team1WrestlerIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get team 1 wrestlers"})
			return
		}

		team2Wrestlers, err := getWrestlers(db, req.Team2WrestlerIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get team 2 wrestlers"})
			return
		}

		if len(team1Wrestlers) != len(req.Team1WrestlerIDs) || len(team2Wrestlers) != len(req.Team2WrestlerIDs) {
			c.JSON(http.StatusNotFound, gin.H{"error": "One or more wrestlers not found"})
			return
		}

		// Calculate effective team ratings with handicap adjustment
		// For uneven teams: effective = midpoint between participant avg and
		// team avg (total ELO / intended team size), where intended = max of both sides
		intendedSize := len(team1Wrestlers)
		if len(team2Wrestlers) > intendedSize {
			intendedSize = len(team2Wrestlers)
		}

		team1AvgELO, team1AvgMomentum := calculateHandicapAverages(team1Wrestlers, intendedSize)
		team2AvgELO, team2AvgMomentum := calculateHandicapAverages(team2Wrestlers, intendedSize)

		// Calculate win probability
		team1Prob, team2Prob := calculateWinProbability(team1AvgELO, team1AvgMomentum, team2AvgELO, team2AvgMomentum)

		response := MatchPredictionResponse{
			Team1Probability: team1Prob,
			Team2Probability: team2Prob,
			Team1AvgELO:      team1AvgELO,
			Team2AvgELO:      team2AvgELO,
			Team1AvgMomentum: team1AvgMomentum,
			Team2AvgMomentum: team2AvgMomentum,
			Team1Wrestlers:   wrestlersToInfo(team1Wrestlers),
			Team2Wrestlers:   wrestlersToInfo(team2Wrestlers),
		}

		c.JSON(http.StatusOK, response)
	}
}

// POST /api/predict/multi
func PredictMulti(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			WrestlerIDs []uint `json:"wrestler_ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
			return
		}
		if len(req.WrestlerIDs) < 3 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Need at least 3 wrestlers"})
			return
		}

		wrestlers, err := getWrestlers(db, req.WrestlerIDs)
		if err != nil || len(wrestlers) != len(req.WrestlerIDs) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "One or more wrestlers not found"})
			return
		}

		// Calculate pairwise win probabilities, then normalize
		// Each wrestler's "strength" = average win probability vs all others
		type MultiResult struct {
			WrestlerInfo
			Probability float64 `json:"probability"`
		}

		results := make([]MultiResult, len(wrestlers))
		for i, w := range wrestlers {
			results[i] = MultiResult{
				WrestlerInfo: WrestlerInfo{
					ID:       w.ID,
					Name:     w.Name,
					ELO:      w.ELO,
					Momentum: w.Momentum,
				},
			}
			totalProb := 0.0
			for j, opp := range wrestlers {
				if i == j {
					continue
				}
				prob, _ := calculateWinProbability(w.ELO, w.Momentum, opp.ELO, opp.Momentum)
				totalProb += prob
			}
			results[i].Probability = totalProb
		}

		// Normalize probabilities to sum to 1
		totalProb := 0.0
		for _, r := range results {
			totalProb += r.Probability
		}
		for i := range results {
			results[i].Probability = results[i].Probability / totalProb
		}

		c.JSON(http.StatusOK, gin.H{"wrestlers": results})
	}
}

// POST /api/predict/tournament
func PredictTournament(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req TournamentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
			return
		}

		if len(req.WrestlerIDs) < 4 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Tournament requires at least 4 wrestlers"})
			return
		}

		wrestlers, err := getWrestlers(db, req.WrestlerIDs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get wrestlers"})
			return
		}

		if len(wrestlers) != len(req.WrestlerIDs) {
			c.JSON(http.StatusNotFound, gin.H{"error": "One or more wrestlers not found"})
			return
		}

		var results interface{}

		switch req.Format {
		case "single_elimination":
			results = simulateSingleElimination(wrestlers, req.Deterministic)
		case "round_robin":
			blockCount := req.BlockCount
			if blockCount == 0 {
				blockCount = 1
			}
			results = simulateRoundRobin(wrestlers, blockCount, req.Deterministic)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid tournament format"})
			return
		}

		response := TournamentResponse{
			Format:  req.Format,
			Results: results,
		}

		c.JSON(http.StatusOK, response)
	}
}

// Helper functions

func getWrestlers(db *gorm.DB, ids []uint) ([]models.Wrestler, error) {
	var wrestlers []models.Wrestler
	err := db.Where("id IN ?", ids).Find(&wrestlers).Error
	if err != nil {
		return nil, err
	}
	// Preserve input order
	wMap := make(map[uint]models.Wrestler)
	for _, w := range wrestlers {
		wMap[w.ID] = w
	}
	ordered := make([]models.Wrestler, 0, len(ids))
	for _, id := range ids {
		if w, ok := wMap[id]; ok {
			ordered = append(ordered, w)
		}
	}
	return ordered, nil
}

// calculateHandicapAverages computes effective team ELO/momentum for uneven teams.
// participantAvg = average of actual members
// teamAvg = total ELO / intendedSize (penalizes missing members)
// effective = midpoint of both
func calculateHandicapAverages(wrestlers []models.Wrestler, intendedSize int) (float64, float64) {
	if len(wrestlers) == 0 {
		return 1200.0, 0.0
	}

	totalELO := 0.0
	totalMomentum := 0.0
	for _, w := range wrestlers {
		totalELO += w.ELO
		totalMomentum += w.Momentum
	}

	participantAvgELO := totalELO / float64(len(wrestlers))
	participantAvgMomentum := totalMomentum / float64(len(wrestlers))

	teamAvgELO := totalELO / float64(intendedSize)
	teamAvgMomentum := totalMomentum / float64(intendedSize)

	return (participantAvgELO + teamAvgELO) / 2, (participantAvgMomentum + teamAvgMomentum) / 2
}

func calculateTeamAverages(wrestlers []models.Wrestler) (float64, float64) {
	if len(wrestlers) == 0 {
		return 1200.0, 0.0
	}

	totalELO := 0.0
	totalMomentum := 0.0

	for _, w := range wrestlers {
		totalELO += w.ELO
		totalMomentum += w.Momentum
	}

	return totalELO / float64(len(wrestlers)), totalMomentum / float64(len(wrestlers))
}

func calculateWinProbability(team1ELO, team1Momentum, team2ELO, team2Momentum float64) (float64, float64) {
	// Base ELO probability (standard formula with SpreadDivisor = 1600)
	eloProb := 1.0 / (1.0 + math.Pow(10, (team2ELO-team1ELO)/1600.0))

	// Momentum adjustment: shift probability by up to ±5% based on momentum difference
	momentumDiff := team1Momentum - team2Momentum
	momentumAdjustment := (momentumDiff / 200.0) * 0.05 // Scale momentum difference to ±5%
	
	// Clamp momentum adjustment to ±5%
	if momentumAdjustment > 0.05 {
		momentumAdjustment = 0.05
	} else if momentumAdjustment < -0.05 {
		momentumAdjustment = -0.05
	}

	team1Prob := eloProb + momentumAdjustment
	
	// Ensure probabilities are in valid range
	if team1Prob > 0.99 {
		team1Prob = 0.99
	} else if team1Prob < 0.01 {
		team1Prob = 0.01
	}

	team2Prob := 1.0 - team1Prob

	return team1Prob, team2Prob
}

func wrestlersToInfo(wrestlers []models.Wrestler) []WrestlerInfo {
	infos := make([]WrestlerInfo, len(wrestlers))
	for i, w := range wrestlers {
		infos[i] = WrestlerInfo{
			ID:       w.ID,
			Name:     w.Name,
			ELO:      w.ELO,
			Momentum: w.Momentum,
		}
	}
	return infos
}

func simulateSingleElimination(wrestlers []models.Wrestler, deterministic bool) SingleElimBracket {
	// Preserve input order — user draws their own bracket matchups
	// (wrestler 1 vs 2, wrestler 3 vs 4, etc.)
	wrestlerInfos := wrestlersToInfo(wrestlers)
	bracket := SingleElimBracket{
		Rounds: [][]BracketMatch{},
	}

	currentRound := make([]*WrestlerInfo, len(wrestlerInfos))
	for i := range wrestlerInfos {
		currentRound[i] = &wrestlerInfos[i]
	}

	// Simulate each round until we have a winner
	for len(currentRound) > 1 {
		roundMatches := []BracketMatch{}
		nextRound := []*WrestlerInfo{}

		// Pair up wrestlers for this round
		for i := 0; i < len(currentRound); i += 2 {
			if i+1 < len(currentRound) {
				w1, w2 := currentRound[i], currentRound[i+1]
				prob1, prob2 := calculateWinProbability(w1.ELO, w1.Momentum, w2.ELO, w2.Momentum)
				
				// Simulate the match
				var winner *WrestlerInfo
				if deterministic {
					if prob1 >= prob2 {
						winner = w1
					} else {
						winner = w2
					}
				} else if rand.Float64() < prob1 {
					winner = w1
				} else {
					winner = w2
				}

				match := BracketMatch{
					Wrestler1:    w1,
					Wrestler2:    w2,
					Winner:       winner,
					Probability1: prob1,
					Probability2: prob2,
				}

				roundMatches = append(roundMatches, match)
				nextRound = append(nextRound, winner)
			} else {
				// Bye - advance directly
				nextRound = append(nextRound, currentRound[i])
			}
		}

		bracket.Rounds = append(bracket.Rounds, roundMatches)
		currentRound = nextRound
	}

	if len(currentRound) > 0 {
		bracket.Winner = *currentRound[0]
	}

	return bracket
}

func simulateRoundRobin(wrestlers []models.Wrestler, blockCount int, deterministic bool) RoundRobinResults {
	wrestlerInfos := wrestlersToInfo(wrestlers)
	
	// Divide wrestlers into blocks
	blockSize := len(wrestlerInfos) / blockCount
	if len(wrestlerInfos)%blockCount != 0 {
		blockSize++
	}

	blocks := []RoundRobinBlock{}

	for b := 0; b < blockCount; b++ {
		start := b * blockSize
		end := start + blockSize
		if end > len(wrestlerInfos) {
			end = len(wrestlerInfos)
		}
		if start >= len(wrestlerInfos) {
			break
		}

		blockWrestlers := wrestlerInfos[start:end]
		if len(blockWrestlers) == 0 {
			continue
		}

		blockName := "Block A"
		if blockCount > 1 {
			blockName = string(rune('A' + b))
		}

		block := simulateRoundRobinBlock(blockWrestlers, blockName, deterministic)
		blocks = append(blocks, block)
	}

	return RoundRobinResults{Blocks: blocks}
}

func simulateRoundRobinBlock(wrestlers []WrestlerInfo, blockName string, deterministic bool) RoundRobinBlock {
	// Initialize results
	results := make(map[uint]RoundRobinResult)
	for _, w := range wrestlers {
		results[w.ID] = RoundRobinResult{
			Wrestler: w,
			Points:   0,
			Wins:     0,
			Losses:   0,
			Draws:    0,
		}
	}

	matches := []RoundRobinMatch{}

	// Simulate all possible matches
	for i := 0; i < len(wrestlers); i++ {
		for j := i + 1; j < len(wrestlers); j++ {
			w1, w2 := wrestlers[i], wrestlers[j]
			prob1, prob2 := calculateWinProbability(w1.ELO, w1.Momentum, w2.ELO, w2.Momentum)

			// Simulate the match
			var winner *WrestlerInfo
			isDraw := false

			if deterministic {
				// Favorite always wins, no draws
				if prob1 >= prob2 {
					winner = &w1
				} else {
					winner = &w2
				}
			} else {
				// Random with 5% draw chance
				r := rand.Float64()
				if r < 0.05 {
					isDraw = true
				} else if r < (0.05 + prob1*0.95) {
					winner = &w1
				} else {
					winner = &w2
				}
			}

			if isDraw {
				result1 := results[w1.ID]
				result1.Draws++
				result1.Points++
				results[w1.ID] = result1

				result2 := results[w2.ID]
				result2.Draws++
				result2.Points++
				results[w2.ID] = result2
			} else if winner.ID == w1.ID {
				result1 := results[w1.ID]
				result1.Wins++
				result1.Points += 2
				results[w1.ID] = result1

				result2 := results[w2.ID]
				result2.Losses++
				results[w2.ID] = result2
			} else {
				result2 := results[w2.ID]
				result2.Wins++
				result2.Points += 2
				results[w2.ID] = result2

				result1 := results[w1.ID]
				result1.Losses++
				results[w1.ID] = result1
			}

			match := RoundRobinMatch{
				Wrestler1:    w1,
				Wrestler2:    w2,
				Winner:       winner,
				Probability1: prob1,
				Probability2: prob2,
				IsDraw:       isDraw,
			}

			matches = append(matches, match)
		}
	}

	// Convert results map to slice and sort by points
	standings := make([]RoundRobinResult, 0, len(results))
	for _, result := range results {
		standings = append(standings, result)
	}

	sort.Slice(standings, func(i, j int) bool {
		if standings[i].Points == standings[j].Points {
			return standings[i].Wins > standings[j].Wins // Tiebreaker: more wins
		}
		return standings[i].Points > standings[j].Points
	})

	return RoundRobinBlock{
		BlockName: blockName,
		Standings: standings,
		Matches:   matches,
	}
}

func init() {
	rand.Seed(time.Now().UnixNano())
}