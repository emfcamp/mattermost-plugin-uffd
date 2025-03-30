package syncablesync

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mattermost/mattermost/server/public/model"
)

var dummyTeamST = SyncableTarget{Type: "Team", ID: "team:::test"}

func TestTeamFetchRoster(t *testing.T) {
	teamMember := &model.TeamMember{
		UserId: "user",
		Roles:  "team_user",
	}
	teamAdmin := &model.TeamMember{
		UserId:      "admin",
		SchemeAdmin: true,
		Roles:       "team_user team_admin",
	}

	h := &mattermostTeamHandler{api: &fakeMattermost{
		teamMembers: map[string][]*model.TeamMember{
			"team:::test": {teamMember, teamAdmin},
		},
	}}
	got, err := h.FetchRoster(context.Background(), dummyTeamST)
	if err != nil {
		t.Fatalf("FetchRoster: %v", err)
	}

	want := []RosterMember{{
		UserID:      "user",
		ServiceType: teamMember,
	}, {
		UserID:      "admin",
		IsAdmin:     true,
		ServiceType: teamAdmin,
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchRoster diff (-got +want):\n%s", diff)
	}
}

func TestTeamAddMembers(t *testing.T) {
	m := &fakeMattermost{}
	h := &mattermostTeamHandler{api: m}

	if err := h.AddMembers(context.Background(), dummyTeamST, []RosterMember{{
		UserID:  "user",
		IsAdmin: false,
	}, {
		UserID:  "admin",
		IsAdmin: true,
	}}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}

	want := map[string][]*model.TeamMember{
		dummyTeamST.ID: {{
			TeamId:     dummyTeamST.ID,
			UserId:     "user",
			SchemeUser: true,
			Roles:      "team_user",
		}, {
			TeamId:      dummyTeamST.ID,
			UserId:      "admin",
			SchemeUser:  true,
			SchemeAdmin: true,
			Roles:       "team_user team_admin",
		}},
	}
	if diff := cmp.Diff(m.teamMembers, want); diff != "" {
		t.Errorf("AddMembers diff (-got +want):\n%s", diff)
	}
}

func TestTeamDeleteMembers(t *testing.T) {
	m := &fakeMattermost{
		teamMembers: map[string][]*model.TeamMember{
			dummyTeamST.ID: {{
				TeamId:     dummyTeamST.ID,
				UserId:     "user",
				SchemeUser: true,
				Roles:      "team_user",
			}, {
				TeamId:      dummyTeamST.ID,
				UserId:      "admin",
				SchemeUser:  true,
				SchemeAdmin: true,
				Roles:       "team_user team_admin",
			}},
		},
	}
	h := &mattermostTeamHandler{api: m}

	if err := h.DeleteMembers(context.Background(), dummyTeamST, []RosterMember{{
		UserID:  "user",
		IsAdmin: false,
	}, {
		UserID:  "admin",
		IsAdmin: true,
	}}); err != nil {
		t.Fatalf("DeleteMembers: %v", err)
	}

	want := map[string][]*model.TeamMember{
		dummyTeamST.ID: nil,
	}
	if diff := cmp.Diff(m.teamMembers, want); diff != "" {
		t.Errorf("DeleteMembers diff (-got +want):\n%s", diff)
	}
}

func TestTeamUpdateMembers(t *testing.T) {
	m := &fakeMattermost{
		teamMembers: map[string][]*model.TeamMember{
			dummyTeamST.ID: {{
				TeamId:     dummyTeamST.ID,
				UserId:     "user",
				SchemeUser: true,
				Roles:      "team_user",
			}, {
				TeamId:     dummyTeamST.ID,
				UserId:     "promote_to_admin",
				SchemeUser: true,
				Roles:      "team_user",
			}, {
				TeamId:      dummyTeamST.ID,
				UserId:      "demote_from_admin",
				SchemeUser:  true,
				SchemeAdmin: true,
				Roles:       "team_user team_admin",
			}},
		},
	}
	h := &mattermostTeamHandler{api: m}

	if err := h.UpdateMembers(context.Background(), dummyTeamST, []RosterMember{{
		UserID:      "promote_to_admin",
		IsAdmin:     true,
		ServiceType: m.teamMembers[dummyTeamST.ID][1],
	}, {
		UserID:      "demote_from_admin",
		IsAdmin:     false,
		ServiceType: m.teamMembers[dummyTeamST.ID][2],
	}}); err != nil {
		t.Fatalf("UpdateMembers: %v", err)
	}

	want := map[string][]*model.TeamMember{
		dummyTeamST.ID: {{
			TeamId:     dummyTeamST.ID,
			UserId:     "user",
			SchemeUser: true,
			Roles:      "team_user",
		}, {
			TeamId:      dummyTeamST.ID,
			UserId:      "promote_to_admin",
			SchemeUser:  true,
			SchemeAdmin: true,
			Roles:       "team_admin team_user",
		}, {
			TeamId:     dummyTeamST.ID,
			UserId:     "demote_from_admin",
			SchemeUser: true,
			Roles:      "team_user",
		}},
	}
	if diff := cmp.Diff(m.teamMembers, want); diff != "" {
		t.Errorf("UpdateMembers diff (-got +want):\n%s", diff)
	}
}
