package syncablesync

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/datastore"
	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

const (
	perPageDefault = 1000
)

type Mattermost struct {
	API        mattermostAPI
	REST       mattermostREST
	GroupStore *datastore.MattermostDataStore

	mu               sync.Mutex
	sessionExpiresAt time.Time

	SystemAdminGroup string
	ManagedTeam      string
}

var _ ServiceAPI = ((*Mattermost)(nil))

type mattermostAPI interface {
	mattermostChannelAPI
	mattermostTeamAPI
	mattermostSystemRoleAPI

	GetTeams() ([]*model.Team, *model.AppError)
	CreateChannel(channel *model.Channel) (*model.Channel, *model.AppError)
	UpdateChannel(channel *model.Channel) (*model.Channel, *model.AppError)

	GetUserByUsername(name string) (*model.User, *model.AppError)
	CreateSession(session *model.Session) (*model.Session, *model.AppError)
}

var _ mattermostAPI = ((plugin.API)(nil))

type mattermostREST interface {
	GetAllChannels(ctx context.Context, page int, perPage int, etag string) (model.ChannelListWithTeamData, *model.Response, error)
	GetScheme(ctx context.Context, id string) (*model.Scheme, *model.Response, error)
}

var _ mattermostREST = ((*model.Client4)(nil))

const (
	mattermostSyncableTypeChannel = SyncableType(model.GroupSyncableTypeChannel)
	mattermostSyncableTypeTeam    = SyncableType(model.GroupSyncableTypeTeam)

	mattermostSyncableTypeSystemRole = SyncableType("system_role")
)

func (m *Mattermost) SortSyncableTargets(st []SyncableTarget) {
	// Teams first, then Channels
	typeOrder := []SyncableType{mattermostSyncableTypeSystemRole, mattermostSyncableTypeTeam, mattermostSyncableTypeChannel}
	slices.SortFunc(st, func(a, b SyncableTarget) int {
		aType, bType := slices.Index(typeOrder, a.Type), slices.Index(typeOrder, b.Type)
		switch {
		case aType < bType:
			return -1
		case aType > bType:
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

func (m *Mattermost) SyncableHandlers() map[SyncableType]SyncableHandler {
	return map[SyncableType]SyncableHandler{
		mattermostSyncableTypeSystemRole: &mattermostSystemRoleHandler{m.API},
		mattermostSyncableTypeChannel:    &mattermostChannelHandler{m.API, m.REST},
		mattermostSyncableTypeTeam:       &mattermostTeamHandler{m.API},
	}
}

func (m *Mattermost) FetchGroupsAndSyncables(ctx context.Context) ([]Group, error) {
	seenUsers := stringset.New()
	var groups []Group
	if m.SystemAdminGroup != "" {
		sg, ok, err := m.GroupStore.LoadGroup(ctx, m.SystemAdminGroup)
		if err != nil {
			return nil, fmt.Errorf("failed to load SystemAdminGroup %q: %w", m.SystemAdminGroup, err)
		} else if !ok {
			return nil, fmt.Errorf("SystemAdminGroup set to %q but no group by that name in the KV store - does it actually exist?", m.SystemAdminGroup)
		}

		seenUsers.Add(sg.MemberUserIDs...)
		groups = append(groups, Group{
			ID:      sg.IDPID,
			Name:    sg.Name,
			Members: sg.MemberUserIDs,
			Syncables: []Syncable{{
				Target: SyncableTarget{
					Type: mattermostSyncableTypeSystemRole,
					ID:   "system_admin",
				},
			}},
		})
	}

	if m.ManagedTeam == "" {
		return nil, fmt.Errorf("ACL syncing is disabled - ManagedTeam configuration key is empty")
	}

	teams, appErr := m.API.GetTeams()
	if appErr != nil {
		return nil, fmt.Errorf("fetching list of teams from Mattermost: %w", appErr)
	}
	var theTeam *model.Team
	for _, team := range teams {
		if team.Name == m.ManagedTeam {
			theTeam = team
		}
	}
	if theTeam == nil {
		return nil, fmt.Errorf("expected a team named %q", m.ManagedTeam)
	}
	// For dumb reasons, getting the list of channels must be done using a bot account, because the plugin can only 'see' public channels.
	if err := m.ensureCredentials(ctx); err != nil {
		return nil, fmt.Errorf("setting up bot account credentials: %w", err)
	}
	etag := ""
	allChannels, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]*model.Channel, error) {
		cs, resp, appErr := m.REST.GetAllChannels(ctx, page, perPage, etag)
		if appErr != nil {
			return nil, appErr
		}
		etag = resp.Etag
		var out []*model.Channel
		for _, c := range cs {
			if c.TeamId != theTeam.Id {
				continue
			}
			out = append(out, &c.Channel)
		}
		return out, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching all channels: %w", err)
	}

	emfTeams, err := m.GroupStore.LoadTeams(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading teams: %w", err)
	}

	// Ensure that we have $TEAM and $TEAM-private channels for all teams.
	channelInfo := map[string]*datastore.ChannelInfo{}
	knownChannels := stringset.New()
	l := ctxlog.FromContext(ctx)
	for _, ch := range allChannels {
		chInfo, ok, err := m.GroupStore.LoadChannel(ctx, ch.Id)
		if err != nil {
			return nil, fmt.Errorf("loading channel info for %s / %s: %w", ch.Id, ch.Name, err)
		} else if !ok {
			chInfo = &datastore.ChannelInfo{
				ID:                  ch.Id,
				Name:                ch.Name,
				MembershipUnmanaged: false,
			}
			if err := m.GroupStore.SaveChannel(ctx, chInfo); err != nil {
				l.WithError(err).WithField("channelInfo", chInfo).Error("failed to save additional channel info")
			}
		}
		channelInfo[ch.Id] = chInfo
		knownChannels.Add(ch.Name)
	}
	for _, ch := range allChannels {
		chInfo := channelInfo[ch.Id]
		if chInfo == nil {
			continue
		}
		wantDisplayName := chInfo.Name
		// HACK: we want the town-square's display name not to match.
		if chInfo.Name == "town-square" {
			wantDisplayName = "emf-all"
		}
		if ch.Name != chInfo.Name || ch.DisplayName != wantDisplayName {
			ch.Name = chInfo.Name
			ch.DisplayName = wantDisplayName
			ch, appErr = m.API.UpdateChannel(ch)
			if appErr != nil {
				l.WithField("channel", ch).WithError(appErr).Error("failed to rename channel back to enforce naming convention")
			}
		}
	}
	for _, t := range emfTeams {
		if !knownChannels.Contains(t.Name) {
			// Create the default public channel for this team.
			ch, appErr := m.API.CreateChannel(&model.Channel{
				TeamId:      theTeam.Id,
				Type:        model.ChannelTypeOpen,
				Name:        t.Name,
				DisplayName: t.Name,
			})
			if appErr != nil {
				return nil, fmt.Errorf("creating default public channel for team %v: %w", t.Name, appErr)
			}
			allChannels = append(allChannels, ch)
			if err := m.GroupStore.SaveChannel(ctx, &datastore.ChannelInfo{
				ID:                  ch.Id,
				Name:                t.Name,
				MembershipUnmanaged: false,
			}); err != nil {
				l.WithError(err).WithField("channel_id", ch.Id).WithField("channel_name", t.Name).Error("Failed to create default public channel metadata")
			}
		}
		privName := t.Name + "-private"
		if !knownChannels.Contains(privName) {
			// Create the default private channel for this team.
			ch, appErr := m.API.CreateChannel(&model.Channel{
				TeamId:      theTeam.Id,
				Type:        model.ChannelTypePrivate,
				Name:        privName,
				DisplayName: privName,
			})
			if appErr != nil {
				return nil, fmt.Errorf("creating default private channel %v for team %v: %w", privName, t.Name, appErr)
			}
			allChannels = append(allChannels, ch)
			if err := m.GroupStore.SaveChannel(ctx, &datastore.ChannelInfo{
				ID:                  ch.Id,
				Name:                privName,
				MembershipUnmanaged: false,
			}); err != nil {
				l.WithError(err).WithField("channel_id", ch.Id).WithField("channel_name", t.Name).Error("Failed to create default private channel metadata")
			}
		}
	}

	allLeads := stringset.New()
	for _, emfTeam := range emfTeams {
		seenUsers.Add(emfTeam.Leads...)
		seenUsers.Add(emfTeam.Members...)

		teamPrefix := emfTeam.Name + "-"
		var leadSyncables []Syncable
		var memberSyncables []Syncable
		for _, ch := range allChannels {
			chInfo := channelInfo[ch.Id]
			if ch.Name == emfTeam.Name || strings.HasPrefix(ch.Name, teamPrefix) {
				t := SyncableTarget{
					Type: mattermostSyncableTypeChannel,
					ID:   ch.Id,
				}
				leadSyncables = append(leadSyncables, Syncable{
					Target:      t,
					GrantsAdmin: true,
				})
				if !chInfo.MembershipUnmanaged {
					memberSyncables = append(memberSyncables, Syncable{
						Target:      t,
						GrantsAdmin: false,
					})
				}
			}
		}

		id := "team-" + emfTeam.Name
		allLeads.Add(emfTeam.Leads...)
		groups = append(groups, Group{
			ID:        id + "-leads",
			Name:      id + "-leads",
			Members:   emfTeam.Leads,
			Syncables: leadSyncables,
		})
		groups = append(groups, Group{
			ID:        id,
			Name:      id,
			Members:   emfTeam.Members,
			Syncables: memberSyncables,
		})
	}

	// Get team, and ensure all members are a member of it, and the emf-all/emf-announce/emf-offtopic channels.
	var leadSyncables []Syncable
	var allUsersSyncables []Syncable
	allUsersSyncables = append(allUsersSyncables, Syncable{Target: SyncableTarget{
		Type: mattermostSyncableTypeTeam,
		ID:   theTeam.Id,
	}})
	for _, ch := range allChannels {
		t := SyncableTarget{
			Type: mattermostSyncableTypeChannel,
			ID:   ch.Id,
		}
		switch ch.Name {
		case "emf-announce":
			leadSyncables = append(leadSyncables, Syncable{
				Target:      t,
				GrantsAdmin: true,
			})
			fallthrough
		case "emf-all": // no emf-offtopic here, we just let people autojoin that
			allUsersSyncables = append(allUsersSyncables, Syncable{
				Target: t,
			})
		case "emf-leads":
			leadSyncables = append(leadSyncables, Syncable{
				Target: t,
			})
		}
	}
	groups = append(groups, Group{
		ID:        "all-users",
		Name:      "all-users",
		Members:   seenUsers.Sorted(),
		Syncables: allUsersSyncables,
	})
	groups = append(groups, Group{
		ID:        "all-leads",
		Name:      "all-leads",
		Members:   allLeads.Sorted(),
		Syncables: leadSyncables,
	})

	return groups, nil
}

func (m *Mattermost) getSystemBot(ctx context.Context) (*model.User, error) {
	l := ctxlog.FromContext(ctx)
	u, appErr := m.API.GetUserByUsername(model.BotSystemBotUsername)
	if appErr != nil {
		l.WithError(appErr).Errorf("getting system bot user (looking for username %q)", model.BotSystemBotUsername)
		return nil, appErr
	}
	return u, nil
}

func (m *Mattermost) ensureCredentials(ctx context.Context) error {
	client, ok := m.REST.(*model.Client4)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	// Is the session still valid?
	if client.AuthToken != "" {
		if now.After(m.sessionExpiresAt) {
			client.AuthToken = ""
			m.sessionExpiresAt = time.Time{}
		} else {
			return nil
		}
	}

	u, err := m.getSystemBot(ctx)
	if err != nil {
		return fmt.Errorf("getSystemBot: %w", err)
	}

	roles := u.GetRawRoles()
	if !u.IsInRole("system_admin") {
		roles += " system_admin"
	}
	sess := &model.Session{
		UserId:    u.Id,
		Roles:     roles,
		DeviceId:  "",
		IsOAuth:   false,
		CreateAt:  now.UnixMilli(),
		ExpiresAt: now.Add(6 * time.Hour).UnixMilli(),
	}
	sess.GenerateCSRF()
	sess, appErr := m.API.CreateSession(sess)
	if appErr != nil {
		return fmt.Errorf("CreateSession for system bot: %w", appErr)
	}
	m.sessionExpiresAt = time.UnixMilli(sess.ExpiresAt).Add(-30 * time.Minute) // add a safety margin to refresh the session early
	client.SetToken(sess.Token)
	return nil
}
