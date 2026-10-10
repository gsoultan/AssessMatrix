package api

import "testing"

// Invitation links open the candidate runner, on its own origin when one is set.
func TestInviteURLOpensTheRunner(t *testing.T) {
	s := &Server{PublicURL: "https://studio.example.id/"}
	if got := s.inviteURL("T"); got != "https://studio.example.id/take/#/i/T" {
		t.Errorf("without a runner origin: %s", got)
	}
	s.RunnerURL = "https://ujian.example.id"
	if got := s.inviteURL("T"); got != "https://ujian.example.id/take/#/i/T" {
		t.Errorf("with a runner origin: %s", got)
	}
}
