package syncablesync

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mattermost/mattermost/server/public/model"
)

var dummyChannelST = SyncableTarget{Type: "Channel", ID: "channel:::test"}

func TestChannelFetchRoster(t *testing.T) {
	channelMember := &model.ChannelMember{
		UserId: "user",
		Roles:  "channel_user",
	}
	channelAdmin := &model.ChannelMember{
		UserId:      "admin",
		SchemeAdmin: true,
		Roles:       "channel_user channel_admin",
	}

	h := &mattermostChannelHandler{api: &fakeMattermost{
		channelMembers: map[string][]*model.ChannelMember{
			"channel:::test": {channelMember, channelAdmin},
		},
	}}
	got, err := h.FetchRoster(context.Background(), dummyChannelST)
	if err != nil {
		t.Fatalf("FetchRoster: %v", err)
	}

	want := []RosterMember{{
		UserID:      "user",
		ServiceType: channelMember,
	}, {
		UserID:      "admin",
		IsAdmin:     true,
		ServiceType: channelAdmin,
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchRoster diff (-got +want):\n%s", diff)
	}
}

func TestChannelAddMembers(t *testing.T) {
	m := &fakeMattermost{
		channels: map[string]*model.Channel{
			"channel:::test": {
				Id: "channel:::test",
			},
		},
	}
	h := &mattermostChannelHandler{api: m}

	if err := h.AddMembers(context.Background(), dummyChannelST, []RosterMember{{
		UserID:  "user",
		IsAdmin: false,
	}, {
		UserID:  "admin",
		IsAdmin: true,
	}}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}

	want := map[string][]*model.ChannelMember{
		dummyChannelST.ID: {{
			ChannelId:  dummyChannelST.ID,
			UserId:     "user",
			SchemeUser: true,
			Roles:      "channel_user",
		}, {
			ChannelId:   dummyChannelST.ID,
			UserId:      "admin",
			SchemeUser:  true,
			SchemeAdmin: true,
			Roles:       "channel_user channel_admin",
		}},
	}
	if diff := cmp.Diff(m.channelMembers, want); diff != "" {
		t.Errorf("AddMembers diff (-got +want):\n%s", diff)
	}
}

func TestChannelDeleteMembers(t *testing.T) {
	m := &fakeMattermost{
		channelMembers: map[string][]*model.ChannelMember{
			dummyChannelST.ID: {{
				ChannelId:  dummyChannelST.ID,
				UserId:     "user",
				SchemeUser: true,
				Roles:      "channel_user",
			}, {
				ChannelId:   dummyChannelST.ID,
				UserId:      "admin",
				SchemeUser:  true,
				SchemeAdmin: true,
				Roles:       "channel_user channel_admin",
			}},
		},
	}
	h := &mattermostChannelHandler{api: m}

	if err := h.DeleteMembers(context.Background(), dummyChannelST, []RosterMember{{
		UserID:  "user",
		IsAdmin: false,
	}, {
		UserID:  "admin",
		IsAdmin: true,
	}}); err != nil {
		t.Fatalf("DeleteMembers: %v", err)
	}

	want := map[string][]*model.ChannelMember{
		dummyChannelST.ID: nil,
	}
	if diff := cmp.Diff(m.channelMembers, want); diff != "" {
		t.Errorf("DeleteMembers diff (-got +want):\n%s", diff)
	}
}

func TestChannelUpdateMembers(t *testing.T) {
	m := &fakeMattermost{
		channels: map[string]*model.Channel{
			"channel:::test": {
				Id: "channel:::test",
			},
		},
		channelMembers: map[string][]*model.ChannelMember{
			dummyChannelST.ID: {{
				ChannelId:  dummyChannelST.ID,
				UserId:     "user",
				SchemeUser: true,
				Roles:      "channel_user",
			}, {
				ChannelId:  dummyChannelST.ID,
				UserId:     "promote_to_admin",
				SchemeUser: true,
				Roles:      "channel_user",
			}, {
				ChannelId:   dummyChannelST.ID,
				UserId:      "demote_from_admin",
				SchemeUser:  true,
				SchemeAdmin: true,
				Roles:       "channel_user channel_admin",
			}},
		},
	}
	h := &mattermostChannelHandler{api: m}

	if err := h.UpdateMembers(context.Background(), dummyChannelST, []RosterMember{{
		UserID:      "promote_to_admin",
		IsAdmin:     true,
		ServiceType: m.channelMembers[dummyChannelST.ID][1],
	}, {
		UserID:      "demote_from_admin",
		IsAdmin:     false,
		ServiceType: m.channelMembers[dummyChannelST.ID][2],
	}}); err != nil {
		t.Fatalf("UpdateMembers: %v", err)
	}

	want := map[string][]*model.ChannelMember{
		dummyChannelST.ID: {{
			ChannelId:  dummyChannelST.ID,
			UserId:     "user",
			SchemeUser: true,
			Roles:      "channel_user",
		}, {
			ChannelId:   dummyChannelST.ID,
			UserId:      "promote_to_admin",
			SchemeUser:  true,
			SchemeAdmin: true,
			Roles:       "channel_admin channel_user",
		}, {
			ChannelId:  dummyChannelST.ID,
			UserId:     "demote_from_admin",
			SchemeUser: true,
			Roles:      "channel_user",
		}},
	}
	if diff := cmp.Diff(m.channelMembers, want); diff != "" {
		t.Errorf("UpdateMembers diff (-got +want):\n%s", diff)
	}
}
