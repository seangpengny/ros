package lobby

import "testing"

func TestLobbyReadyAndAllocation(t *testing.T) {
	service, err := NewService(2)
	if err != nil {
		t.Fatal(err)
	}
	lobby, err := service.Create("owner", "squad", "ap-southeast")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Join(lobby.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if lobby, err = service.SetReady(lobby.ID, "owner", true); err != nil || lobby.Status != StatusOpen {
		t.Fatalf("owner ready transition = %+v err=%v", lobby, err)
	}
	if lobby, err = service.SetReady(lobby.ID, "member", true); err != nil || lobby.Status != StatusReady {
		t.Fatalf("all-ready transition = %+v err=%v", lobby, err)
	}
	if lobby, err = service.Allocate(lobby.ID); err != nil || lobby.Status != StatusAllocated {
		t.Fatalf("allocation transition = %+v err=%v", lobby, err)
	}
}

func TestLobbyCapacityAndStateGuards(t *testing.T) {
	service, err := NewService(1)
	if err != nil {
		t.Fatal(err)
	}
	lobby, err := service.Create("owner", "solo", "na")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Join(lobby.ID, "extra"); err == nil {
		t.Fatal("expected capacity error")
	}
	if _, err := service.Allocate(lobby.ID); err == nil {
		t.Fatal("expected non-ready allocation error")
	}
}

func TestLobbyInvitationsAndPresence(t *testing.T) {
	service, err := NewService(2)
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Create("owner", "duo", "ap-southeast")
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := service.Invite(value.ID, "owner", "member")
	if err != nil || invitation.Status != InvitationPending {
		t.Fatalf("invite = %+v err=%v", invitation, err)
	}
	if duplicate, err := service.Invite(value.ID, "owner", "member"); err != nil || duplicate.ID != invitation.ID {
		t.Fatalf("duplicate invite = %+v err=%v", duplicate, err)
	}
	if _, err := service.AcceptInvitation(invitation.ID, "other"); err == nil {
		t.Fatal("expected recipient authorization error")
	}
	inbox, err := service.ListInvitations("member")
	if err != nil || len(inbox) != 1 || inbox[0].ID != invitation.ID {
		t.Fatalf("inbox = %+v err=%v", inbox, err)
	}
	joined, err := service.AcceptInvitation(invitation.ID, "member")
	if err != nil || len(joined.Members) != 2 || joined.Members[1].AccountID != "member" {
		t.Fatalf("accepted lobby = %+v err=%v", joined, err)
	}
	if inbox, err = service.ListInvitations("member"); err != nil || len(inbox) != 0 {
		t.Fatalf("accepted invitation remained pending: %+v err=%v", inbox, err)
	}
	presence, err := service.SetPresence("member", true)
	if err != nil || !presence.Online || presence.LastSeen.IsZero() {
		t.Fatalf("online presence = %+v err=%v", presence, err)
	}
	presence, err = service.GetPresence("member")
	if err != nil || !presence.Online {
		t.Fatalf("stored presence = %+v err=%v", presence, err)
	}
	presence, err = service.SetPresence("member", false)
	if err != nil || presence.Online {
		t.Fatalf("offline presence = %+v err=%v", presence, err)
	}
}
