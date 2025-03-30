package syncablesync

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeMattermost struct {
	channelMembers map[string][]*model.ChannelMember
	teamMembers    map[string][]*model.TeamMember
	syncables      []*model.GroupSyncable
	groups         []*model.Group
	groupMembers   map[string][]string
}

// AddUserToChannel implements mattermostAPI.
func (f *fakeMattermost) AddUserToChannel(channelID string, userID string, onBehalfOfUser string) (*model.ChannelMember, *model.AppError) {
	if f.channelMembers == nil {
		f.channelMembers = make(map[string][]*model.ChannelMember)
	}
	m := &model.ChannelMember{
		ChannelId:  channelID,
		UserId:     userID,
		SchemeUser: true,
		Roles:      "channel_user",
	}
	f.channelMembers[channelID] = append(f.channelMembers[channelID], m)
	return m, nil
}

// CreateTeamMembers implements mattermostAPI.
func (f *fakeMattermost) CreateTeamMembers(teamID string, userIDs []string, onBehalfOfUser string) ([]*model.TeamMember, *model.AppError) {
	if f.teamMembers == nil {
		f.teamMembers = make(map[string][]*model.TeamMember)
	}
	var out []*model.TeamMember
	for _, userID := range userIDs {
		m := &model.TeamMember{
			TeamId:     teamID,
			UserId:     userID,
			SchemeUser: true,
			Roles:      "team_user",
		}
		f.teamMembers[teamID] = append(f.teamMembers[teamID], m)
		out = append(out, m)
	}
	return out, nil
}

// DeleteChannelMember implements mattermostAPI.
func (f *fakeMattermost) DeleteChannelMember(channelID string, userID string) *model.AppError {
	var newChannelMembers []*model.ChannelMember
	for _, m := range f.channelMembers[channelID] {
		if m.UserId != userID {
			newChannelMembers = append(newChannelMembers, m)
		}
	}
	f.channelMembers[channelID] = newChannelMembers
	return nil
}

// DeleteTeamMember implements mattermostAPI.
func (f *fakeMattermost) DeleteTeamMember(teamID string, userID string, onBehalfOfUser string) *model.AppError {
	var newTeamMembers []*model.TeamMember
	for _, m := range f.teamMembers[teamID] {
		if m.UserId != userID {
			newTeamMembers = append(newTeamMembers, m)
		}
	}
	f.teamMembers[teamID] = newTeamMembers
	return nil
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

// GetChannelMembers implements mattermostAPI.
func (f *fakeMattermost) GetChannelMembers(channelID string, page int, perPage int) (model.ChannelMembers, *model.AppError) {
	mems := slicePage(f.channelMembers[channelID], page, perPage)
	out := make([]model.ChannelMember, 0, len(mems))
	for _, m := range mems {
		out = append(out, *m)
	}
	return out, nil
}

// GetGroupMemberUsers implements mattermostAPI.
func (f *fakeMattermost) GetGroupMemberUsers(groupID string, page int, perPage int) ([]*model.User, *model.AppError) {
	us := slicePage(f.groupMembers[groupID], page, perPage)
	var out []*model.User
	for _, u := range us {
		out = append(out, &model.User{
			Id: u,
		})
	}
	return out, nil
}

// GetGroupSyncables implements mattermostAPI.
func (f *fakeMattermost) GetGroupSyncables(groupID string, syncableType model.GroupSyncableType) ([]*model.GroupSyncable, *model.AppError) {
	var out []*model.GroupSyncable
	for _, s := range f.syncables {
		if s.GroupId == groupID && s.Type == syncableType {
			out = append(out, s)
		}
	}
	return out, nil
}

// GetGroupsBySource implements mattermostAPI.
func (f *fakeMattermost) GetGroupsBySource(model.GroupSource) ([]*model.Group, *model.AppError) {
	return f.groups, nil
}

// GetTeamMembers implements mattermostAPI.
func (f *fakeMattermost) GetTeamMembers(teamID string, page int, perPage int) ([]*model.TeamMember, *model.AppError) {
	return slicePage(f.teamMembers[teamID], page, perPage), nil
}

// UpdateChannelMemberRoles implements mattermostAPI.
func (f *fakeMattermost) UpdateChannelMemberRoles(channelID string, userID string, roles string) (*model.ChannelMember, *model.AppError) {
	for _, mem := range f.channelMembers[channelID] {
		if mem.UserId == userID {
			mem.Roles = roles
			rs := stringset.FromSlice(mem.GetRoles())
			mem.SchemeGuest = rs.Contains("channel_guest")
			mem.SchemeUser = rs.Contains("channel_user")
			mem.SchemeAdmin = rs.Contains("channel_admin")
			return mem, nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such member", 404)
}

// UpdateTeamMemberRoles implements mattermostAPI.
func (f *fakeMattermost) UpdateTeamMemberRoles(teamID string, userID string, roles string) (*model.TeamMember, *model.AppError) {
	for _, mem := range f.teamMembers[teamID] {
		if mem.UserId == userID {
			mem.Roles = roles
			rs := stringset.FromSlice(mem.GetRoles())
			mem.SchemeGuest = rs.Contains("team_guest")
			mem.SchemeUser = rs.Contains("team_user")
			mem.SchemeAdmin = rs.Contains("team_admin")
			return mem, nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such member", 404)
}

var _ mattermostAPI = ((*fakeMattermost)(nil))

func TestFetchGroupsAndSyncables(t *testing.T) {
	testGroup := &model.Group{
		Id:           "group:::test",
		HasSyncables: true,
	}
	testGroupChannelSyncable := &model.GroupSyncable{
		GroupId:     "group:::test",
		SchemeAdmin: false,
		Type:        model.GroupSyncableTypeChannel,
		SyncableId:  "channel:::test",
	}
	testGroupTeamSyncable := &model.GroupSyncable{
		GroupId:     "group:::test",
		SchemeAdmin: false,
		Type:        model.GroupSyncableTypeTeam,
		SyncableId:  "team:::test",
	}

	testAdminGroup := &model.Group{
		Id:           "group:::admin",
		HasSyncables: true,
	}
	testAdminGroupChannelSyncable := &model.GroupSyncable{
		GroupId:     "group:::admin",
		SchemeAdmin: true,
		Type:        model.GroupSyncableTypeChannel,
		SyncableId:  "channel:::test",
	}
	testAdminGroupTeamSyncable := &model.GroupSyncable{
		GroupId:     "group:::admin",
		SchemeAdmin: true,
		Type:        model.GroupSyncableTypeTeam,
		SyncableId:  "team:::test",
	}
	m := &Mattermost{
		API: &fakeMattermost{
			groups: []*model.Group{
				testGroup,
				testAdminGroup,
			},
			groupMembers: map[string][]string{
				"group:::test":  {"userBoth", "userUser"},
				"group:::admin": {"userBoth", "userAdmin"},
			},
			syncables: []*model.GroupSyncable{
				testGroupChannelSyncable,
				testGroupTeamSyncable,
				testAdminGroupChannelSyncable,
				testAdminGroupTeamSyncable,
			},
		},
	}
	got, err := m.FetchGroupsAndSyncables(context.Background())
	if err != nil {
		t.Fatalf("FetchGroupsAndSyncables: %v", err)
	}

	want := []Group{{
		ID:      "group:::test",
		Members: []string{"userBoth", "userUser"},
		Syncables: []Syncable{
			{Target: SyncableTarget{Type: "Channel", ID: "channel:::test"}, ServiceType: testGroupChannelSyncable},
			{Target: SyncableTarget{Type: "Team", ID: "team:::test"}, ServiceType: testGroupTeamSyncable},
		},
		ServiceType: testGroup,
	}, {
		ID:      "group:::admin",
		Members: []string{"userBoth", "userAdmin"},
		Syncables: []Syncable{
			{
				Target:      SyncableTarget{Type: "Channel", ID: "channel:::test"},
				ServiceType: testAdminGroupChannelSyncable,
				GrantsAdmin: true,
			},
			{
				Target:      SyncableTarget{Type: "Team", ID: "team:::test"},
				ServiceType: testAdminGroupTeamSyncable,
				GrantsAdmin: true,
			},
		},
		ServiceType: testAdminGroup,
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchGroupsAndSyncables diff (-got +want):\n%s", diff)
	}
}
