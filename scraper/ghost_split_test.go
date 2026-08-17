package scraper

import (
	"reflect"
	"testing"
)

// Cases taken verbatim from junk ghost_name rows written to the production DB
// during the 2026-08 catchup scrape.
func TestSplitGhostSegmentObservedJunk(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		// pure result/annotation fragments → dropped entirely
		{") (c)", nil},
		{") (8:52)", nil},
		{"))", nil},
		{"???)", nil},
		{"Time Limit Draw (15:00)", nil},
		{"by DQ [2:1] (8:45)", nil},
		{"Draw [1:1] (10:12)", nil},
		{"Double KO (11:34)", nil},
		// stray brackets around real names → cleaned
		{"Matt Skyler)", []string{"Matt Skyler"}},
		{"Lady Shadow)", []string{"Lady Shadow"}},
		{"Therapy (", []string{"Therapy"}},
		{"Atsuko Maeda (", []string{"Atsuko Maeda"}},
		{"Full St-Emo Space Machino (", []string{"Full St-Emo Space Machino"}},
		{"Zhen Shuang Quan Hao (10:15)", []string{"Zhen Shuang Quan Hao"}},
		// unbalanced stable-listing fragments → stable and member recovered
		{"Therapy (Gunther Isaak", []string{"Therapy", "Gunther Isaak"}},
		{"Team APEX (Jack Holmes", []string{"Team APEX", "Jack Holmes"}},
		{"Team Rocket (Jessie (", []string{"Team Rocket", "Jessie"}},
		// balanced gimmick/real-name parens → kept whole
		{"Luigi (Brandon Dillinger)", []string{"Luigi (Brandon Dillinger)"}},
	}
	for _, tc := range cases {
		got := SplitGhostSegment(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitGhostSegment(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
