package models

import (
	"time"
)

// TODO: Wrestler struct + WrestlerAlias struct + WrestlerStore interface
type Wrestler struct {
	ID uint `json:"id" gorm:"primaryKey"`
	CagematchID uint `json:"cagematch_id" gorm:"uniqueIndex"`

	//personal info
	Name string `json:"name" gorm:"not null"`
	Birthday time.Time `json:"birthday,omitempty"`
	Birthplace string `json:"birthplace,omitempty"`
	Height string `json:"height,omitempty"`
	Weight string `json:"weight,omitempty"`
	DebutYear int `json:"debut_year,omitempty"`
	WrestlingStyle string `json:"wrestling_style,omitempty"`
	Promotion string `json:"promotion" gorm:"not null"`
	Socials []Socials `json:"socials,omitempty" gorm:"foreignKey:WrestlerID"`
	Aliases []WrestlerAlias `json:"aliases" gorm:"foreignKey:WrestlerID"`
	SignatureMoves []string `json:"signature_moves,omitempty" gorm:"serializer:json"`	
	
	//stats
	ELO           float64    `json:"elo" gorm:"default:1000"`
	Momentum      float64    `json:"momentum" gorm:"default:0"`
	LastMatchDate *time.Time `json:"last_match_date,omitempty"`
	LastScrapedAt *time.Time `json:"last_scraped_at,omitempty"`
	MatchCount int `json:"match_count" gorm:"default:0"`
	Wins int `json:"wins" gorm:"default:0"`
	Losses int `json:"losses" gorm:"default:0"`
	Draws int `json:"draws" gorm:"default:0"`

	//region (derived from promotion history)
	CurrentRegion string `json:"current_region,omitempty"` // country of most common promotion in last 50 matches
	GeneralRegion string `json:"general_region,omitempty"` // country of most common promotion overall

	//extra
	ImageURL   string `json:"image"`
	ImageLocal string `json:"image_local" gorm:"column:image_local"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
}

type WrestlerAlias struct {
	ID uint `json:"id" gorm:"primaryKey"`
	WrestlerID uint `json:"wrestler_id" gorm:"index"`
	Alias string `json:"alias" gorm:"not null"`
}
type Socials struct {
	ID uint `json:"id" gorm:"primaryKey"`
	WrestlerID uint `json:"wrestler_id" gorm:"index"`
	Name string `json:"name" gorm:"not null"`
	URL string `json:"url" gorm:"not null"`
}

type WrestlerStore interface {
    GetAll(promotion string) ([]Wrestler, error)
    GetByID(id uint) (Wrestler, error)
    Create(w Wrestler) (Wrestler, error)
    Update(w Wrestler) (Wrestler, error)
    Delete(id uint) error
}

type Ranking struct {
	Rank     int      `json:"rank"`
	Tier     string   `json:"tier"`
	TierIcon string   `json:"tier_icon"`
	Wrestler Wrestler `json:"wrestler"`
	Trends   Trends   `json:"trends"`
}

type Trends struct {
	Last2 string `json:"last_2"`
	Last5 string `json:"last_5"`
	Last10 string `json:"last_10"`
	Streak int `json:"streak"`
} 

// SkippedWrestler persists CagematchIDs we know are male so we don't re-check.
type SkippedWrestler struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	CagematchID int    `json:"cagematch_id" gorm:"uniqueIndex"`
	Name        string `json:"name"` // just for reference/debugging
}

// Promotion stores scraped promotion data from Cagematch.
type Promotion struct {
	ID             uint   `json:"id" gorm:"primaryKey"`
	CagematchID    int    `json:"cagematch_id" gorm:"uniqueIndex"`
	Name           string `json:"name"`
	Abbreviation   string `json:"abbreviation"`
	Location       string `json:"location"`       // e.g. "Tokyo, Japan"
	Country        string `json:"country"`         // e.g. "Japan"
	Region         string `json:"region"`          // e.g. "Asia", "North America", "Europe"
	Status         string `json:"status"`          // "Active" or "Inactive"
	ActiveFrom     string `json:"active_from"`
	ActiveTo       string `json:"active_to"`
}

// PromotionHistory tracks which promotions a wrestler worked for each year.
// Scraped from Cagematch page=20 (Matches per Promotion and Year).
type PromotionHistory struct {
	ID          uint   `json:"id" gorm:"primaryKey"`
	WrestlerID  uint   `json:"wrestler_id" gorm:"index:idx_promo_hist,unique"`
	Promotion   string `json:"promotion" gorm:"index:idx_promo_hist,unique"`
	PromotionID int    `json:"promotion_id"` // Cagematch promotion ID
	Year        int    `json:"year" gorm:"index:idx_promo_hist,unique"`
	Matches     int    `json:"matches"`
}

// TitleReign tracks a wrestler's title reign.
// Scraped from Cagematch page=11 (Titles).
type TitleReign struct {
	ID               uint       `json:"id" gorm:"primaryKey"`
	WrestlerID       uint       `json:"wrestler_id" gorm:"index"`
	TitleName        string     `json:"title_name" gorm:"index"`
	CagematchTitleID int        `json:"cagematch_title_id" gorm:"index"`
	ReignNumber      int        `json:"reign_number"`
	WonDate          time.Time  `json:"won_date" gorm:"index"`
	LostDate         *time.Time `json:"lost_date,omitempty"`
	DurationDays     int        `json:"duration_days"`
	Untracked        bool       `json:"untracked" gorm:"default:false"` // true for male/unknown holders in intergender titles
	HolderName       string     `json:"holder_name,omitempty"`          // original name from Cagematch (useful for untracked)
	HolderCagematchID int       `json:"holder_cagematch_id,omitempty"`  // Cagematch ID of holder (even if not in wrestlers table)
}

// Title stores metadata about a championship scraped from Cagematch.
type Title struct {
	ID             uint   `json:"id" gorm:"primaryKey"`
	CagematchID    int    `json:"cagematch_id" gorm:"uniqueIndex"`
	Name           string `json:"name"`
	Promotion      string `json:"promotion"`
	PromotionID    int    `json:"promotion_id"`
	Status         string `json:"status"`          // "Active", "Inactive"
	HasFemaleReign bool   `json:"has_female_reign"` // at least one female holder
}

type ELOHistory struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	WrestlerID uint      `json:"wrestler_id" gorm:"index"`
	ELO        float64   `json:"elo" gorm:"not null"`
	MatchID    uint      `json:"match_id" gorm:"index"`
	MatchDate  time.Time `json:"match_date" gorm:"index"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

type MomentumHistory struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	WrestlerID uint      `json:"wrestler_id" gorm:"index"`
	Momentum   float64   `json:"momentum"`
	MatchID    uint      `json:"match_id" gorm:"index"`
	MatchDate  time.Time `json:"match_date" gorm:"index"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

// User represents an authenticated user of the system.
type User struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	Username     string     `json:"username" gorm:"uniqueIndex;not null"`
	PasswordHash string     `json:"-" gorm:"not null"`
	DisplayName  string     `json:"display_name"`
	Role         string     `json:"role" gorm:"default:user"`
	CreatedAt    time.Time  `json:"created_at" gorm:"autoCreateTime"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}