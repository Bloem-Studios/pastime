package bloem

import (
	"errors"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

func TestCalendarHandoffSettingsRequireAdminAndSafeOrigins(t *testing.T) {
	f, s, instance, _ := identityFixture(t, bloemIssuer)
	member := f.User(t, "handoff_member", "member")
	settings := HandoffSettings{Enabled: true, Label: "Watch <here>", BaseURL: "https://player.example.test"}
	if _, err := s.SetHandoffSettings(t.Context(), member, instance.ID, settings); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member edited", err)
	}
	for _, bad := range []string{"javascript:alert(1)", "https://u:p@example.test", "https://example.test/path", "https://example.test?token=x", "https://example.test#x", "https://example.test/{id}", "//example.test"} {
		in := settings
		in.BaseURL = bad
		if _, err := s.SetHandoffSettings(t.Context(), f.Owner, instance.ID, in); err == nil {
			t.Fatal("unsafe origin accepted", bad)
		}
	}
	out, err := s.SetHandoffSettings(t.Context(), f.Owner, instance.ID, settings)
	if err != nil || out != settings {
		t.Fatal(out, err)
	}
	if auditCount(t, f, "bloem.instance.handoff") != 1 {
		t.Fatal("edit not audited")
	}
	link := handoffTarget(out, "movie/a?b#c")
	if link == nil || link.URL != "https://player.example.test/item/movie%2Fa%3Fb%23c" || link.Label != settings.Label {
		t.Fatal(link)
	}
	out.Enabled = false
	if handoffTarget(out, "item") != nil {
		t.Fatal("disabled target returned")
	}
	out.Enabled = true
	out.BaseURL = ""
	if handoffTarget(out, "item") != nil {
		t.Fatal("unconfigured target returned")
	}
}
