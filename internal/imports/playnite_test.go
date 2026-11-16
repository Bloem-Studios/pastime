package imports

import (
	"errors"
	"testing"
)

func TestParsePlayniteMergesStoresAndSkipsUnplayed(t *testing.T) {
	csv := "\xef\xbb\xbfName,Source,Game Id,Completion Status,TimePlayedHours\n" +
		"Hades,Steam,1145360,Beaten,\"31,5\"\n" +
		"HADES!,Epic,abc,Played,2\n" +
		"Celeste,GOG,1,Plan to Play,0\n" +
		"Untouched,Steam,99,Not Played,0\n" +
		"Outer Wilds,Epic,x,,0.01\n" +
		",Steam,5,Played,3\n"
	recs, err := ParsePlaynite([]byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("records %+v", recs)
	}
	hades, celeste, wilds := recs[0], recs[1], recs[2]
	if hades.Label != "Hades" || hades.Status != "completed" || hades.Progress == nil || *hades.Progress != 1890 ||
		hades.External == nil || hades.External.Scheme != "steam" || hades.External.Value != "1145360" || hades.Kind != "game" {
		t.Fatalf("hades %+v", hades)
	}
	if celeste.Status != "planned" || celeste.Progress != nil || celeste.External != nil {
		t.Fatalf("celeste %+v", celeste)
	}
	if wilds.Status != "in_progress" || wilds.Progress == nil || *wilds.Progress != 1 {
		t.Fatalf("outer wilds %+v", wilds)
	}
}

func TestParsePlayniteSecondsColumnAndErrors(t *testing.T) {
	recs, err := ParsePlaynite([]byte("Name,CompletionStatus,Time Played\nPortal,Completed,5400\n"))
	if err != nil || len(recs) != 1 || *recs[0].Progress != 90 || recs[0].Status != "completed" {
		t.Fatalf("seconds variant %+v %v", recs, err)
	}
	for _, bad := range []string{"Title,Status\nX,Played\n", "Name,TimePlayedHours\nX,lots\n", ""} {
		if _, err = ParsePlaynite([]byte(bad)); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
