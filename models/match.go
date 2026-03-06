package models

import "time"

type Match struct {
	ID               uint               `json:"id" gorm:"primaryKey"`
	MatchType        string             `json:"match_type" gorm:"not null"`
	EventName        string             `json:"event_name" gorm:"not null"`
	Date             time.Time          `json:"date" gorm:"not null"`
	IsTitleMatch     bool               `json:"is_title_match" gorm:"default:false"`
	IsDraw           bool               `json:"is_draw" gorm:"default:false"`
	MatchTime        string             `json:"match_time,omitempty"`   // e.g. "25:09"
	FinishType       string             `json:"finish_type,omitempty"`  // e.g. "Pinfall", "Submission", "Count Out"
	Venue            string             `json:"venue,omitempty"`        // e.g. "Korakuen Hall"
	Location         string             `json:"location,omitempty"`     // e.g. "Tokyo, Japan"
	Promotion        string             `json:"promotion,omitempty"`    // promotion that ran the event
	Stipulation      string             `json:"stipulation,omitempty"`  // e.g. "No DQ", "Cage Match"
	CagematchEventID int                `json:"cagematch_event_id" gorm:"index"`
	MatchIndex       int                `json:"match_index"`
	MatchKey         string             `json:"match_key" gorm:"uniqueIndex;size:512"`
	CreatedAt        time.Time          `json:"created_at" gorm:"autoCreateTime"`
	Participants     []MatchParticipant `json:"participants" gorm:"foreignKey:MatchID"`
}

type MatchParticipant struct {
	ID uint `json:"id" gorm:"primaryKey"`
	MatchID uint `json:"match_id" gorm:"index"`
	WrestlerID uint `json:"wrestler_id" gorm:"index"`
	Team int `json:"team"` // 1 for team A, 2 for team B, 3 ... 
	IsWinner bool `json:"is_winner"`
	EloChange float64 `json:"elo_change" gorm:"default:0"`
	GhostName string `json:"ghost_name,omitempty"`             // name for untracked (male) participants
	GhostCagematchID int `json:"ghost_cagematch_id,omitempty"` // cagematch ID for ghost participants
}	
type MatchStore interface {
    GetAll(wrestlerID uint) ([]Match, error)
    GetByID(id uint) (Match, error)
    Create(m Match) (Match, error)
    Delete(id uint) error
}