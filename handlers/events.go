package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EventMatch struct {
	MatchID      uint                  `json:"match_id"`
	MatchType    string                `json:"match_type"`
	IsTitleMatch bool                  `json:"is_title_match"`
	IsDraw       bool                  `json:"is_draw"`
	Participants []EventParticipant    `json:"participants"`
}

type EventParticipant struct {
	Name             string  `json:"name"`
	WrestlerID       uint    `json:"wrestler_id"`
	GhostCagematchID int     `json:"ghost_cagematch_id"`
	Team             int     `json:"team"`
	IsWinner         bool    `json:"is_winner"`
	ELOBefore        float64 `json:"elo_before"`
	ELOChange        float64 `json:"elo_change"`
}

type EventResponse struct {
	CagematchEventID int          `json:"cagematch_event_id"`
	EventName        string       `json:"event_name"`
	Date             string       `json:"date"`
	Promotion        string       `json:"promotion"`
	MatchCount       int          `json:"match_count"`
	Matches          []EventMatch `json:"matches"`
}

func GetEvent(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		eventID := c.Param("id")

		// Get event info from first match
		type eventInfo struct {
			EventName        string
			CagematchEventID int
			Date             string
			Promotion        string
		}
		var info eventInfo
		err := db.Raw(`
			SELECT event_name, cagematch_event_id, date,
				COALESCE((SELECT w.promotion FROM match_participants mp2
					JOIN wrestlers w ON w.id = mp2.wrestler_id
					WHERE mp2.match_id = m.id AND w.promotion != ''
					LIMIT 1), '') as promotion
			FROM matches m
			WHERE m.cagematch_event_id = ?
			ORDER BY m.id ASC
			LIMIT 1
		`, eventID).Scan(&info).Error
		if err != nil || info.EventName == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}

		// Get all matches for this event
		type matchRow struct {
			ID           uint
			MatchType    string
			IsTitleMatch bool
			IsDraw       bool
		}
		var matches []matchRow
		db.Raw(`
			SELECT id, match_type, is_title_match, is_draw
			FROM matches
			WHERE cagematch_event_id = ?
			ORDER BY id ASC
		`, eventID).Scan(&matches)

		result := make([]EventMatch, 0, len(matches))
		for _, m := range matches {
			var participants []EventParticipant
			db.Raw(`
				SELECT COALESCE(w.name, mp.ghost_name) as name,
					mp.wrestler_id,
					COALESCE(mp.ghost_cagematch_id, 0) as ghost_cagematch_id,
					mp.team, mp.is_winner, mp.elo_change,
					CASE WHEN mp.wrestler_id > 0 THEN (w.elo - mp.elo_change) ELSE 0 END as elo_before
				FROM match_participants mp
				LEFT JOIN wrestlers w ON w.id = mp.wrestler_id
				WHERE mp.match_id = ?
				ORDER BY mp.team, mp.is_winner DESC, mp.id
			`, m.ID).Scan(&participants)

			result = append(result, EventMatch{
				MatchID:      m.ID,
				MatchType:    m.MatchType,
				IsTitleMatch: m.IsTitleMatch,
				IsDraw:       m.IsDraw,
				Participants: participants,
			})
		}

		c.JSON(http.StatusOK, EventResponse{
			CagematchEventID: info.CagematchEventID,
			EventName:        info.EventName,
			Date:             info.Date,
			Promotion:        info.Promotion,
			MatchCount:       len(result),
			Matches:          result,
		})
	}
}
