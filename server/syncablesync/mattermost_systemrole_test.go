package syncablesync

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
)

var dummySystemRoleST = SyncableTarget{Type: mattermostSyncableTypeSystemRole, ID: "system_admin"}

func TestSystemRoleFetchRoster(t *testing.T) {
	admin := &model.User{
		Id:          "user:::admin",
		Username:    "admin",
		Roles:       "system_user system_admin",
		AuthService: "openid",
		Props: map[string]string{
			syncengine.MMIdPUserIDProp: "1000",
		},
	}
	ignoredAdmin := &model.User{
		Id:          "user:::ignoredadmin",
		Username:    "ignoredadmin",
		Roles:       "system_user system_admin",
		AuthService: "passwordOrSomething",
	}
	h := &mattermostSystemRoleHandler{api: &fakeMattermost{
		users: []*model.User{admin, ignoredAdmin},
	}}
	got, err := h.FetchRoster(context.Background(), dummySystemRoleST)
	if err != nil {
		t.Fatalf("FetchRoster: %v", err)
	}

	want := []RosterMember{{
		UserID:      "user:::admin",
		ServiceType: admin,
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchRoster diff (-got +want):\n%s", diff)
	}
}

func TestSystemRoleAddMembers(t *testing.T) {
	m := &fakeMattermost{
		users: []*model.User{{
			Id:          "user:::admin",
			Username:    "admin",
			Roles:       "system_user",
			AuthService: "openid",
			Props: map[string]string{
				syncengine.MMIdPUserIDProp: "1000",
			},
		}},
	}
	h := &mattermostSystemRoleHandler{api: m}

	if err := h.AddMembers(context.Background(), dummySystemRoleST, []RosterMember{{
		UserID: "user:::admin",
	}}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}

	want := []*model.User{{
		Id:          "user:::admin",
		Username:    "admin",
		Roles:       "system_admin system_user",
		AuthService: "openid",
		Props: map[string]string{
			syncengine.MMIdPUserIDProp: "1000",
		},
	}}
	if diff := cmp.Diff(m.users, want); diff != "" {
		t.Errorf("AddMembers diff (-got +want):\n%s", diff)
	}
}

func TestSystemRoleDeleteMembers(t *testing.T) {
	m := &fakeMattermost{
		users: []*model.User{{
			Id:          "user:::admin",
			Username:    "admin",
			Roles:       "system_admin system_user",
			AuthService: "openid",
			Props: map[string]string{
				syncengine.MMIdPUserIDProp: "1000",
			},
		}},
	}
	h := &mattermostSystemRoleHandler{api: m}

	if err := h.DeleteMembers(context.Background(), dummySystemRoleST, []RosterMember{{
		UserID: "user:::admin",
	}}); err != nil {
		t.Fatalf("DeleteMembers: %v", err)
	}

	want := []*model.User{{
		Id:          "user:::admin",
		Username:    "admin",
		Roles:       "system_user",
		AuthService: "openid",
		Props: map[string]string{
			syncengine.MMIdPUserIDProp: "1000",
		},
	}}
	if diff := cmp.Diff(m.users, want); diff != "" {
		t.Errorf("DeleteMembers diff (-got +want):\n%s", diff)
	}
}

func TestSystemRoleUpdateMembers(t *testing.T) {
	m := &fakeMattermost{
		users: []*model.User{{
			Id:          "user:::admin",
			Username:    "admin",
			Roles:       "system_admin system_user",
			AuthService: "openid",
			Props: map[string]string{
				syncengine.MMIdPUserIDProp: "1000",
			},
		}},
	}
	h := &mattermostSystemRoleHandler{api: m}

	err := h.UpdateMembers(context.Background(), dummySystemRoleST, []RosterMember{{
		UserID: "user:::admin",
	}})
	if err == nil {
		t.Fatalf("UpdateMembers: expected err, got %v", err)
	}
}
