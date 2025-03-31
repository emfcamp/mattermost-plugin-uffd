package syncengine

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeMattermostPluginAPI struct {
	Groups       []*model.Group
	Users        []*model.User
	GroupMembers []*model.GroupMember
}

// CreateGroup implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) CreateGroup(g *model.Group) (*model.Group, *model.AppError) {
	groupCopy := *g
	groupCopy.Id = fmt.Sprintf("id::group::%s", g.GetName())
	f.Groups = append(f.Groups, &groupCopy)
	groupCopy2 := groupCopy
	return &groupCopy2, nil
}

// CreateUser implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) CreateUser(u *model.User) (*model.User, *model.AppError) {
	u = u.DeepCopy()
	u.Id = fmt.Sprintf("id::user::%v", u.Username)
	f.Users = append(f.Users, u.DeepCopy())
	return u.DeepCopy(), nil
}

// DeleteGroup implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) DeleteGroup(groupID string) (*model.Group, *model.AppError) {
	newGroups := make([]*model.Group, 0, len(f.Groups)-1)
	var foundGroup *model.Group
	for _, g := range f.Groups {
		if g.Id == groupID {
			foundGroup = g
			continue
		}
		newGroups = append(newGroups, g)
	}
	if foundGroup == nil {
		return nil, model.NewAppError("test", "test", nil, "no group found", 404)
	}
	f.Groups = newGroups
	foundGroupCopy := *foundGroup
	return &foundGroupCopy, nil
}

// DeleteGroupMember implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) DeleteGroupMember(groupID string, memberID string) (*model.GroupMember, *model.AppError) {
	newMembers := make([]*model.GroupMember, 0, len(f.GroupMembers)-1)
	var foundMember *model.GroupMember
	for _, gm := range f.GroupMembers {
		if gm.GroupId == groupID && gm.UserId == memberID {
			foundMember = gm
			continue
		}
		newMembers = append(newMembers, gm)
	}
	if foundMember == nil {
		return nil, model.NewAppError("test", "test", nil, "no member found", 404)
	}
	f.GroupMembers = newMembers
	return foundMember, nil
}

func slicePage[T any](slice []T, page int, perPage int) []T {
	start := perPage * page
	end := perPage * (page + 1)
	if start > len(slice) {
		return nil
	}
	if end > len(slice) {
		end = len(slice)
	}
	return slice[start:end]
}

// GetGroupMemberUsers implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetGroupMemberUsers(groupID string, page int, perPage int) ([]*model.User, *model.AppError) {
	var allMembers []*model.GroupMember
	for _, gm := range f.GroupMembers {
		if gm.GroupId == groupID {
			allMembers = append(allMembers, gm)
		}
	}
	var out []*model.User
	for _, gm := range slicePage(allMembers, page, perPage) {
		for _, u := range f.Users {
			if u.Id == gm.UserId {
				out = append(out, u.DeepCopy())
			}
		}
	}
	return out, nil
}

func xmap[T any](xs []T, f func(t T) T) []T {
	out := make([]T, len(xs))
	for n, x := range xs {
		out[n] = f(x)
	}
	return out
}

func copyGroups(gs []*model.Group) []*model.Group {
	return xmap(gs, func(g *model.Group) *model.Group {
		gs := *g
		return &gs
	})
}

// GetGroupsBySource implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetGroupsBySource(source model.GroupSource) ([]*model.Group, *model.AppError) {
	return copyGroups(f.Groups), nil
}

// GetUser implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetUser(userID string) (*model.User, *model.AppError) {
	for _, u := range f.Users {
		if u.Id == userID {
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// GetUsers implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetUsers(opts *model.UserGetOptions) ([]*model.User, *model.AppError) {
	return xmap(slicePage(f.Users, opts.Page, opts.PerPage), (*model.User).DeepCopy), nil
}

// UpdateUser implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) UpdateUser(in *model.User) (*model.User, *model.AppError) {
	for _, u := range f.Users {
		if u.Id == in.Id {
			u.Username = in.Username
			u.Email = in.Email
			u.EmailVerified = in.EmailVerified
			u.Nickname = in.Nickname
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// UpdateUserActive implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) UpdateUserActive(userID string, active bool) *model.AppError {
	for _, u := range f.Users {
		if u.Id == userID {
			if active {
				u.DeleteAt = 0
			} else {
				u.DeleteAt = 1000
			}
			return nil
		}
	}
	return model.NewAppError("test", "test", nil, "no such user", 404)
}

// UpsertGroupMembers implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) UpsertGroupMembers(groupID string, memberIDs []string) ([]*model.GroupMember, *model.AppError) {
	out := make([]*model.GroupMember, len(memberIDs))

	needMemberIDs := make(map[string]int, len(memberIDs))
	for n, mid := range memberIDs {
		needMemberIDs[mid] = n
	}

	for _, gm := range f.GroupMembers {
		if gm.GroupId == groupID {
			pos, ok := needMemberIDs[gm.UserId]
			if ok {
				delete(needMemberIDs, gm.UserId)
				gmCopy := *gm
				out[pos] = &gmCopy
			}
		}
	}

	for mid, pos := range needMemberIDs {
		gm := &model.GroupMember{
			GroupId: groupID,
			UserId:  mid,
		}
		f.GroupMembers = append(f.GroupMembers, gm)
		gmCopy := *gm
		out[pos] = &gmCopy
	}

	return out, nil
}

var _ mattermostPluginAPI = (*fakeMattermostPluginAPI)(nil)

func TestMattermostFetchUsers(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:          "id::user::testuser",
			Username:    "testuser",
			Nickname:    "Test User",
			Email:       "testuser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1000",
			},
		}, {
			Id:          "id::user::disableduser",
			Username:    "disableduser",
			Nickname:    "Disabled User",
			Email:       "disableduser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1001",
			},
			DeleteAt: 1000,
		}, {
			Id:          "id::user::systemuser",
			Username:    "systemuser",
			Nickname:    "System User",
			Email:       "systemuser@example.com",
			AuthService: "password",
		}},
	}
	m := &MattermostService{p}

	want := []*User[string]{{
		UserID:        "id::user::testuser",
		Username:      "testuser",
		DisplayName:   "Test User",
		Email:         "testuser@example.com",
		Active:        true,
		IDPUserID:     1000,
		ServiceUserID: "id::user::testuser",
		ServiceUser:   p.Users[0],
	}, {
		UserID:        "id::user::disableduser",
		Username:      "disableduser",
		DisplayName:   "Disabled User",
		Email:         "disableduser@example.com",
		Active:        false,
		IDPUserID:     1001,
		ServiceUserID: "id::user::disableduser",
		ServiceUser:   p.Users[1],
	}}

	got, err := m.FetchUsers(context.Background())
	if err != nil {
		t.Fatalf("FetchUsers: %v", err)
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchUsers diff (-got +want):\n%s", diff)
	}
}

func ptr[T any](t T) *T { return &t }

func TestMattermostFetchGroups(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:          "id::user::testuser",
			Username:    "testuser",
			Nickname:    "Test User",
			Email:       "testuser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1000",
			},
		}},
		Groups: []*model.Group{{
			Id:          "id::group::testgroup",
			Name:        ptr("testgroup"),
			DisplayName: "testgroup displayname",
			Description: "testgroup description",
			RemoteId:    ptr("remote-testgroup"),
			Source:      MMPluginSource,
		}, {
			Id:          "id::group::emptygroup",
			Name:        ptr("emptygroup"),
			DisplayName: "emptygroup displayname",
			Description: "emptygroup description",
			RemoteId:    ptr("remote-emptygroup"),
			Source:      MMPluginSource,
		}},
		GroupMembers: []*model.GroupMember{{
			GroupId: "id::group::testgroup",
			UserId:  "id::user::testuser",
		}},
	}
	m := &MattermostService{p}

	want := []*Group[string]{{
		GroupID:       "id::group::testgroup",
		Name:          "testgroup",
		IDPID:         "remote-testgroup",
		MemberUserIDs: []string{"id::user::testuser"},
		ServiceGroup:  p.Groups[0],
	}, {
		GroupID:      "id::group::emptygroup",
		Name:         "emptygroup",
		IDPID:        "remote-emptygroup",
		ServiceGroup: p.Groups[1],
	}}

	got, err := m.FetchGroups(context.Background())
	if err != nil {
		t.Fatalf("FetchGroups: %v", err)
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchGroups diff (-got +want):\n%s", diff)
	}
}

func TestMattermostCreateUsers(t *testing.T) {
	p := &fakeMattermostPluginAPI{}
	m := &MattermostService{p}

	got, err := m.CreateUsers(context.Background(), []*User[int]{{
		UserID:      1000,
		Username:    "testuser",
		DisplayName: "Test User",
		Email:       "testuser@example.com",
		Active:      true,
	}, {
		UserID:      1001,
		Username:    "disableduser",
		DisplayName: "Disabled User",
		Email:       "disableduser@example.com",
		Active:      false,
	}})
	if err != nil {
		t.Fatalf("CreateUsers: %v", err)
	}

	want := []*User[string]{{
		UserID:      "id::user::testuser",
		Username:    "testuser",
		DisplayName: "Test User",
		Email:       "testuser@example.com",
		Active:      true,

		IDPUserID:     1000,
		ServiceUserID: "id::user::testuser",
		ServiceUser: &model.User{
			Id:            "id::user::testuser",
			Username:      "testuser",
			Nickname:      "Test User",
			AuthData:      ptr("1000"),
			AuthService:   "openid",
			Email:         "testuser@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "testuser",
			},
			DisableWelcomeEmail: true,
		},
	}, {
		UserID:      "id::user::disableduser",
		Username:    "disableduser",
		DisplayName: "Disabled User",
		Email:       "disableduser@example.com",
		Active:      false,

		IDPUserID:     1001,
		ServiceUserID: "id::user::disableduser",
		ServiceUser: &model.User{
			Id:            "id::user::disableduser",
			Username:      "disableduser",
			Nickname:      "Disabled User",
			AuthData:      ptr("1001"),
			AuthService:   "openid",
			Email:         "disableduser@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1001",
				MMIdPUsernameProp: "disableduser",
			},
			DisableWelcomeEmail: true,
			DeleteAt:            1000,
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("CreateUsers diff (-got +want):\n%s", diff)
	}
}

func TestMattermostCreateGroups(t *testing.T) {
	p := &fakeMattermostPluginAPI{}
	m := &MattermostService{p}

	got, err := m.CreateGroups(context.Background(), []*Group[int]{{
		GroupID:       1000,
		Name:          "testgroup",
		MemberUserIDs: []int{1000, 1001},
	}, {
		GroupID: 5000,
		Name:    "testgroup2",
	}})
	if err != nil {
		t.Fatalf("CreateGroups: %v", err)
	}

	want := []*Group[string]{{
		GroupID: "id::group::testgroup",
		Name:    "testgroup",
		IDPID:   "1000",
		ServiceGroup: &model.Group{
			Id:          "id::group::testgroup",
			Name:        ptr("testgroup"),
			DisplayName: "testgroup",
			Description: "uffd group testgroup",
			Source:      MMPluginSource,
			RemoteId:    ptr("1000"),
		},
	}, {
		GroupID: "id::group::testgroup2",
		Name:    "testgroup2",
		IDPID:   "5000",
		ServiceGroup: &model.Group{
			Id:          "id::group::testgroup2",
			Name:        ptr("testgroup2"),
			DisplayName: "testgroup2",
			Description: "uffd group testgroup2",
			Source:      MMPluginSource,
			RemoteId:    ptr("5000"),
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("CreateGroups diff (-got +want):\n%s", diff)
	}
}

func TestMattermostUpdateUsers(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:            "id::user::testuser",
			Username:      "testuser",
			Nickname:      "Test User",
			AuthService:   "openid",
			Email:         "testuser@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "testuser",
			},
			DisableWelcomeEmail: true,
		}, {
			Id:            "id::user::testuser2",
			Username:      "testuser2",
			Nickname:      "Test User 2",
			AuthService:   "openid",
			Email:         "testuser2@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1001",
				MMIdPUsernameProp: "testuser2",
			},
			DisableWelcomeEmail: true,
		}, {
			Id:            "id::user::disableduser",
			Username:      "disableduser",
			Nickname:      "Reenabled User",
			AuthService:   "openid",
			Email:         "disableduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1002",
				MMIdPUsernameProp: "disableduser",
			},
			DisableWelcomeEmail: true,
			DeleteAt:            1000,
		}, {
			Id:            "id::user::untoucheduser",
			Username:      "untoucheduser",
			Nickname:      "Untouched User",
			AuthService:   "openid",
			Email:         "untoucheduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1003",
				MMIdPUsernameProp: "untoucheduser",
			},
			DisableWelcomeEmail: true,
		}},
	}
	m := &MattermostService{p}

	got, updated, err := m.UpdateUsers(context.Background(), []*User[string]{{
		UserID:      "id::user::testuser",
		Username:    "nowdisabled",
		DisplayName: "New Display Name",
		Email:       "newemail@example.bin",
		Active:      false,
	}, {
		UserID:      "id::user::testuser2",
		Username:    "newuser2",
		DisplayName: "New Display Name 2",
		Email:       "newemail2@example.bin",
		Active:      true,
	}, {
		UserID:      "id::user::disableduser",
		Username:    "disableduser",
		DisplayName: "Reenabled User",
		Email:       "disableduser@example.bin",
		Active:      true,
	}, {
		UserID:      "id::user::untoucheduser",
		Username:    "untoucheduser",
		DisplayName: "Untouched User",
		Email:       "untoucheduser@example.bin",
		Active:      true,
	}})
	if err != nil {
		t.Fatalf("UpdateUsers: %v", err)
	}

	want := []*User[string]{{
		UserID:      "id::user::testuser",
		Username:    "nowdisabled",
		DisplayName: "New Display Name",
		Email:       "newemail@example.bin",
		Active:      false,

		IDPUserID:     1000,
		ServiceUserID: "id::user::testuser",
		ServiceUser: &model.User{
			Id:            "id::user::testuser",
			Username:      "nowdisabled",
			Nickname:      "New Display Name",
			AuthService:   "openid",
			Email:         "newemail@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "testuser",
			},
			DisableWelcomeEmail: true,
			DeleteAt:            1000,
		},
	}, {
		UserID:      "id::user::testuser2",
		Username:    "newuser2",
		DisplayName: "New Display Name 2",
		Email:       "newemail2@example.bin",
		Active:      true,

		IDPUserID:     1001,
		ServiceUserID: "id::user::testuser2",
		ServiceUser: &model.User{
			Id:            "id::user::testuser2",
			Username:      "newuser2",
			Nickname:      "New Display Name 2",
			AuthService:   "openid",
			Email:         "newemail2@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1001",
				MMIdPUsernameProp: "testuser2",
			},
			DisableWelcomeEmail: true,
		},
	}, {
		UserID:      "id::user::disableduser",
		Username:    "disableduser",
		DisplayName: "Reenabled User",
		Email:       "disableduser@example.bin",
		Active:      true,

		IDPUserID:     1002,
		ServiceUserID: "id::user::disableduser",
		ServiceUser: &model.User{
			Id:            "id::user::disableduser",
			Username:      "disableduser",
			Nickname:      "Reenabled User",
			AuthService:   "openid",
			Email:         "disableduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1002",
				MMIdPUsernameProp: "disableduser",
			},
			DisableWelcomeEmail: true,
		},
	}, {
		UserID:      "id::user::untoucheduser",
		Username:    "untoucheduser",
		DisplayName: "Untouched User",
		Email:       "untoucheduser@example.bin",
		Active:      true,

		IDPUserID:     1003,
		ServiceUserID: "id::user::untoucheduser",
		ServiceUser: &model.User{
			Id:            "id::user::untoucheduser",
			Username:      "untoucheduser",
			Nickname:      "Untouched User",
			AuthService:   "openid",
			Email:         "untoucheduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1003",
				MMIdPUsernameProp: "untoucheduser",
			},
			DisableWelcomeEmail: true,
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("UpdateUsers diff (-got +want):\n%s", diff)
	}

	wantUpdated := []bool{
		true, true, true, false,
	}
	if diff := cmp.Diff(updated, wantUpdated); diff != "" {
		t.Fatalf("UpdateUsers updated diff (-got +want):\n%s", diff)
	}
}

func TestMattermostDeleteGroups(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Groups: []*model.Group{{
			Id:          "id::group::testgroup",
			Name:        ptr("testgroup"),
			DisplayName: "testgroup displayname",
			Description: "testgroup description",
			RemoteId:    ptr("remote-testgroup"),
			Source:      MMPluginSource,
		}, {
			Id:          "id::group::emptygroup",
			Name:        ptr("emptygroup"),
			DisplayName: "emptygroup displayname",
			Description: "emptygroup description",
			RemoteId:    ptr("remote-emptygroup"),
			Source:      MMPluginSource,
		}},
	}
	m := &MattermostService{p}

	err := m.DeleteGroups(context.Background(), []*Group[string]{{
		GroupID: "id::group::testgroup",
		Name:    "testgroup",
		IDPID:   "remote-testgroup",
	}, {
		GroupID: "id::group::emptygroup",
		Name:    "emptygroup",
		IDPID:   "remote-emptygroup",
	}})
	if err != nil {
		t.Fatalf("DeleteGroups: %v", err)
	}

	if len(p.Groups) > 0 {
		t.Fatalf("DeleteGroups didn't delete the groups")
	}
}

func TestMattermostAddGroupMembers(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:          "id::user::testuser",
			Username:    "testuser",
			Nickname:    "Test User",
			Email:       "testuser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1000",
			},
		}},
		Groups: []*model.Group{{
			Id:          "id::group::testgroup",
			Name:        ptr("testgroup"),
			DisplayName: "testgroup displayname",
			Description: "testgroup description",
			RemoteId:    ptr("remote-testgroup"),
			Source:      MMPluginSource,
		}},
	}
	m := &MattermostService{p}

	err := m.AddGroupMembers(context.Background(), "id::group::testgroup", []string{"id::user::testuser"})
	if err != nil {
		t.Fatalf("AddGroupMembers: %v", err)
	}

	want := []*model.GroupMember{{
		UserId:  "id::user::testuser",
		GroupId: "id::group::testgroup",
	}}
	if diff := cmp.Diff(want, p.GroupMembers); diff != "" {
		t.Errorf("AddGroupMembers diff (-want +got):\n%s", diff)
	}
}

func TestMattermostRemoveGroupMembers(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:          "id::user::testuser",
			Username:    "testuser",
			Nickname:    "Test User",
			Email:       "testuser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1000",
			},
		}},
		Groups: []*model.Group{{
			Id:          "id::group::testgroup",
			Name:        ptr("testgroup"),
			DisplayName: "testgroup displayname",
			Description: "testgroup description",
			RemoteId:    ptr("remote-testgroup"),
			Source:      MMPluginSource,
		}},
		GroupMembers: []*model.GroupMember{{
			UserId:  "id::user::testuser",
			GroupId: "id::group::testgroup",
		}},
	}
	m := &MattermostService{p}

	err := m.RemoveGroupMembers(context.Background(), "id::group::testgroup", []string{"id::user::testuser"})
	if err != nil {
		t.Fatalf("RemoveGroupMembers: %v", err)
	}

	if len(p.GroupMembers) != 0 {
		t.Errorf("RemoveGroupMembers didn't remove the group members")
	}
}
