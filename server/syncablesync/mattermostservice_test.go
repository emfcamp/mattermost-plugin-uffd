package syncablesync

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mattermost/mattermost/server/public/model"

	"github.com/lukegb/mattermost-plugin-uffd/server/datastore"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
)

type fakeMattermost struct {
	channels       map[string]*model.Channel
	channelMembers map[string][]*model.ChannelMember
	teamMembers    map[string][]*model.TeamMember
	users          []*model.User
	teams          []*model.Team

	kvStore map[string][]byte
}

// UpdateChannel implements mattermostAPI.
func (f *fakeMattermost) UpdateChannel(channel *model.Channel) (*model.Channel, *model.AppError) {
	for n, ch := range f.channels {
		if ch.Id == channel.Id {
			f.channels[n] = channel.DeepCopy()
			return f.channels[n], nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "channel not found", 404)
}

// GetUserByUsername implements mattermostAPI.
func (f *fakeMattermost) GetUserByUsername(name string) (*model.User, *model.AppError) {
	if name != model.BotSystemBotUsername {
		return nil, model.NewAppError("test", "test", nil, "bad username", 400)
	}
	return &model.User{
		Id:    "system bot",
		Roles: "foo",
	}, nil
}

// CreateSession implements mattermostAPI.
func (f *fakeMattermost) CreateSession(session *model.Session) (*model.Session, *model.AppError) {
	session = session.DeepCopy()
	session.Token = rand.Text()
	return session, nil
}

// GetTeams implements mattermostAPI.
func (f *fakeMattermost) GetTeams() ([]*model.Team, *model.AppError) {
	var out []*model.Team
	for _, t := range f.teams {
		out = append(out, t.ShallowCopy())
	}
	return out, nil
}

// GetChannel implements mattermostAPI.
func (f *fakeMattermost) GetChannel(channelID string) (*model.Channel, *model.AppError) {
	ch, ok := f.channels[channelID]
	if !ok {
		return nil, model.NewAppError("test", "test", nil, "no such channel", 404)
	}
	return ch, nil
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

// GetUsers implements mattermostAPI.
func (f *fakeMattermost) GetUsers(opts *model.UserGetOptions) ([]*model.User, *model.AppError) {
	var users []*model.User
	for _, u := range f.users {
		matches := true
		if opts.Role != "" {
			matches = matches && u.IsInRole(opts.Role)
		}
		if len(opts.Roles) > 0 {
			matchedRole := false
			for _, r := range opts.Roles {
				matchedRole = matchedRole || u.IsInRole(r)
				if matchedRole {
					break
				}
			}
			matches = matches && matchedRole
		}
		if matches {
			users = append(users, u.DeepCopy())
		}
	}
	return slicePage(users, opts.Page, opts.PerPage), nil
}

// GetUsers implements mattermostAPI.
func (f *fakeMattermost) GetUser(userID string) (*model.User, *model.AppError) {
	for _, u := range f.users {
		if u.Id == userID {
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// UpdateUserRoles implements mattermostAPI.
func (f *fakeMattermost) UpdateUserRoles(userID string, newRoles string) (*model.User, *model.AppError) {
	for _, u := range f.users {
		if u.Id == userID {
			u.Roles = newRoles
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// KVDelete implements mattermostPluginAPI.
func (f *fakeMattermost) KVDelete(key string) *model.AppError {
	if f.kvStore != nil {
		delete(f.kvStore, key)
	}
	return nil
}

// KVGet implements mattermostPluginAPI.
func (f *fakeMattermost) KVGet(key string) ([]byte, *model.AppError) {
	val, ok := f.kvStore[key]
	if !ok {
		return nil, nil
	}
	return val, nil
}

// KVSet implements mattermostPluginAPI.
func (f *fakeMattermost) KVSet(key string, value []byte) *model.AppError {
	if f.kvStore == nil {
		f.kvStore = make(map[string][]byte)
	}
	f.kvStore[key] = value
	return nil
}

// CreateChannel implements mattermostAPI.
func (f *fakeMattermost) CreateChannel(channel *model.Channel) (*model.Channel, *model.AppError) {
	channel = channel.DeepCopy()
	channel.Id = fmt.Sprintf("channel:::%s:::%s", channel.TeamId, channel.Name)
	f.channels[channel.Id] = channel
	return channel, nil
}

var _ mattermostAPI = ((*fakeMattermost)(nil))

// GetScheme implements mattermostREST.
func (f *fakeMattermost) GetScheme(ctx context.Context, id string) (*model.Scheme, *model.Response, error) {
	panic("unimplemented")
}

// GetAllChannels implements mattermostREST.
func (f *fakeMattermost) GetAllChannels(ctx context.Context, page int, perPage int, etag string) (model.ChannelListWithTeamData, *model.Response, error) {
	chs := slices.Collect(maps.Values(f.channels))
	slices.SortFunc(chs, func(a, b *model.Channel) int { return strings.Compare(a.Id, b.Id) })
	var out []*model.ChannelWithTeamData
	for n := page * perPage; n < min(len(chs), (page+1)*perPage); n++ {
		out = append(out, &model.ChannelWithTeamData{
			Channel: *chs[n],
		})
	}
	return out, &model.Response{}, nil
}

var _ mattermostREST = ((*fakeMattermost)(nil))

func TestFetchGroupsAndSyncables(t *testing.T) {
	mm := &fakeMattermost{
		teams: []*model.Team{{
			Id: "emfcamp",
		}},
		channels: map[string]*model.Channel{
			"foo": {
				Id:          "foo",
				Name:        "foo",
				DisplayName: "foo",
				TeamId:      "emfcamp",
			},
			"foo-private": {
				Id:          "foo-private",
				Name:        "foo-private",
				DisplayName: "foo-private",
				TeamId:      "emfcamp",
			},
			"emf-announce": {
				Id:          "emf-announce",
				Name:        "emf-announce",
				DisplayName: "emf-announce",
				TeamId:      "emfcamp",
			},
			"emf-all": {
				Id:          "emf-all",
				Name:        "emf-all",
				DisplayName: "emf-all",
				TeamId:      "emfcamp",
			},
			"emf-offtopic": {
				Id:          "emf-offtopic",
				Name:        "emf-offtopic",
				DisplayName: "emf-offtopic",
				TeamId:      "emfcamp",
			},
			"emf-not-default-channel": {
				Id:          "emf-not-default-channel",
				Name:        "emf-not-default-channel",
				DisplayName: "emf-not-default-channel",
				TeamId:      "emfcamp",
			},
			"unrelated": {
				Id:          "unrelated",
				Name:        "unrelated",
				DisplayName: "unrelated",
				TeamId:      "emfcamp",
			},
		},
	}
	gs := &datastore.MattermostDataStore{API: mm}
	ctx := context.Background()
	if err := gs.SaveGroups(ctx, []*syncengine.Group[string]{{
		GroupID:       "admin",
		Name:          "admin",
		MemberUserIDs: []string{"userAdmin"},
	}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}
	if err := gs.SaveTeams(ctx, []syncengine.Team{{
		Name:    "foo",
		Leads:   []string{"userFooLead"},
		Members: []string{"userFooMember", "userFooLead"},
	}}); err != nil {
		t.Fatalf("SaveTeams: %v", err)
	}
	m := &Mattermost{
		API:        mm,
		REST:       mm,
		GroupStore: gs,

		SystemAdminGroup: "admin",
	}
	got, err := m.FetchGroupsAndSyncables(context.Background())
	if err != nil {
		t.Fatalf("FetchGroupsAndSyncables: %v", err)
	}

	want := []Group{{
		Name:    "admin",
		Members: []string{"userAdmin"},
		Syncables: []Syncable{
			{Target: SyncableTarget{Type: mattermostSyncableTypeSystemRole, ID: "system_admin"}},
		},
	}, {
		ID:      "team-foo-leads",
		Name:    "team-foo-leads",
		Members: []string{"userFooLead"},
		Syncables: []Syncable{
			{
				Target:      SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "foo"},
				GrantsAdmin: true,
			},
			{
				Target:      SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "foo-private"},
				GrantsAdmin: true,
			},
		},
	}, {
		ID:      "team-foo",
		Name:    "team-foo",
		Members: []string{"userFooMember", "userFooLead"},
		Syncables: []Syncable{
			{
				Target: SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "foo"},
			},
			{
				Target: SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "foo-private"},
			},
		},
	}, {
		ID:      "all-users",
		Name:    "all-users",
		Members: []string{"userAdmin", "userFooLead", "userFooMember"},
		Syncables: []Syncable{
			{Target: SyncableTarget{Type: mattermostSyncableTypeTeam, ID: "emfcamp"}},
			{Target: SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "emf-all"}},
			{Target: SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "emf-announce"}},
			{Target: SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "emf-offtopic"}},
		},
	}, {
		ID:      "all-leads",
		Name:    "all-leads",
		Members: []string{"userFooLead"},
		Syncables: []Syncable{
			{Target: SyncableTarget{Type: mattermostSyncableTypeChannel, ID: "emf-announce"}, GrantsAdmin: true},
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchGroupsAndSyncables diff (-got +want):\n%s", diff)
	}
}
