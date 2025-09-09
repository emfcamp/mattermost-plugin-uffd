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

	GetBots(options *model.BotGetOptions) ([]*model.Bot, *model.AppError)
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
	knownChannels := map[string]string{}
	defaultedChannels := stringset.New()
	l := ctxlog.FromContext(ctx)
	for _, ch := range allChannels {
		chInfo, ok, err := m.GroupStore.LoadChannel(ctx, ch.Id)
		if err != nil {
			return nil, fmt.Errorf("loading channel info for %s / %s: %w", ch.Id, ch.Name, err)
		} else if !ok {
			chInfo = &datastore.ChannelInfo{
				ID:                  ch.Id,
				Name:                ch.Name,
				MembershipUnmanaged: true,
			}
			if err := m.GroupStore.SaveChannel(ctx, chInfo); err != nil {
				l.WithError(err).WithField("channelInfo", chInfo).Error("failed to save additional channel info")
			}
			defaultedChannels.Add(ch.Id)
		}
		channelInfo[ch.Id] = chInfo
		knownChannels[ch.Name] = ch.Id
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
		if t.Name == "admin" {
			// Skip team_admin, since that's just leads, and we'll deal with them separately.
			// We don't want them to get auto-created channels.
			continue
		}

		membersACL := []datastore.ACLElement{{
			Type:  datastore.ACLElementTypeTeamMember,
			Value: t.Name,
		}}
		adminsACL := []datastore.ACLElement{{
			Type:  datastore.ACLElementTypeTeamLead,
			Value: t.Name,
		}}
		if _, ok := knownChannels[t.Name]; !ok {
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
			knownChannels[t.Name] = ch.Id
			defaultedChannels.Add(ch.Id)
			channelInfo[ch.Id] = &datastore.ChannelInfo{
				ID:   ch.Id,
				Name: ch.Name,
			}

			// Only try to create the private channel if we created the public one.
			// This ensures that we can delete the private channel if we don't need it.
			privName := t.Name + "-private"
			if _, ok := knownChannels[privName]; !ok {
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
				knownChannels[t.Name] = ch.Id
				defaultedChannels.Add(ch.Id)
				channelInfo[ch.Id] = &datastore.ChannelInfo{
					ID:   ch.Id,
					Name: ch.Name,
				}
			}
		}

		for _, ch := range allChannels {
			if ch.Name != t.Name && !strings.HasPrefix(ch.Name, t.Name+"-") {
				continue
			}
			// This is one of this team's channels.
			if !defaultedChannels.Contains(ch.Id) {
				// If this wasn't defaulted, we have nothing to do.
				continue
			}
			chInfo := channelInfo[ch.Id]
			chInfo.MembershipUnmanaged = ch.IsOpen()
			chInfo.Admins = adminsACL
			chInfo.Members = membersACL
			if err := m.GroupStore.SaveChannel(ctx, chInfo); err != nil {
				l.WithError(err).WithField("channel_id", ch.Id).WithField("channel_name", ch.Name).Error("Failed to create default team channel metadata")
			}
		}
	}

	indexedMembers := make(map[datastore.ACLElement]map[bool][]*datastore.ChannelInfo)
	for _, ch := range allChannels {
		chInfo := channelInfo[ch.Id]
		for _, mem := range chInfo.Members {
			if _, ok := indexedMembers[mem]; !ok {
				indexedMembers[mem] = make(map[bool][]*datastore.ChannelInfo)
			}
			indexedMembers[mem][false] = append(indexedMembers[mem][false], chInfo)
		}
		for _, mem := range chInfo.Admins {
			if _, ok := indexedMembers[mem]; !ok {
				indexedMembers[mem] = make(map[bool][]*datastore.ChannelInfo)
			}
			indexedMembers[mem][true] = append(indexedMembers[mem][true], chInfo)
		}
	}

	allLeads := stringset.New()
	for _, emfTeam := range emfTeams {
		seenUsers.Add(emfTeam.Leads...)
		seenUsers.Add(emfTeam.Members...)

		var leadSyncables []Syncable
		var memberSyncables []Syncable
		for grantsAdmin, mems := range indexedMembers[datastore.ACLElement{Type: datastore.ACLElementTypeTeamLead, Value: emfTeam.Name}] {
			for _, mem := range mems {
				leadSyncables = append(leadSyncables, Syncable{
					Target: SyncableTarget{
						Type: mattermostSyncableTypeChannel,
						ID:   mem.ID,
					},
					GrantsAdmin: grantsAdmin,
				})
			}
		}
		for grantsAdmin, mems := range indexedMembers[datastore.ACLElement{Type: datastore.ACLElementTypeTeamMember, Value: emfTeam.Name}] {
			for _, mem := range mems {
				memberSyncables = append(memberSyncables, Syncable{
					Target: SyncableTarget{
						Type: mattermostSyncableTypeChannel,
						ID:   mem.ID,
					},
					GrantsAdmin: grantsAdmin,
				})
			}
		}

		id := "team-" + emfTeam.Name
		allLeads.Add(emfTeam.Leads...)
		if emfTeam.Name == "admin" {
			allLeads.Add(emfTeam.Members...)
		}
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

	for aclEl, adminToChInfos := range indexedMembers {
		if aclEl.Type == datastore.ACLElementTypeTeamLead || aclEl.Type == datastore.ACLElementTypeTeamMember {
			continue
		}

		var syncables []Syncable
		for grantsAdmin, chInfos := range adminToChInfos {
			for _, chInfo := range chInfos {
				syncables = append(syncables, Syncable{
					Target: SyncableTarget{
						Type: mattermostSyncableTypeChannel,
						ID:   chInfo.ID,
					},
					GrantsAdmin: grantsAdmin,
				})
			}
		}

		switch aclEl.Type {
		case datastore.ACLElementTypeUser:
			groups = append(groups, Group{
				ID:        fmt.Sprintf("user:%s", aclEl.Value),
				Name:      fmt.Sprintf("user:%s", aclEl.Value),
				Members:   []string{aclEl.Value},
				Syncables: syncables,
			})
		case datastore.ACLElementTypeGroup:
			sg, ok, err := m.GroupStore.LoadGroup(ctx, aclEl.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to load group %q: %w", aclEl.Value, err)
			} else if !ok {
				return nil, fmt.Errorf("group in ACL element %q but no group by that name in the KV store - does it actually exist?", aclEl.Value)
			}

			seenUsers.Add(sg.MemberUserIDs...)
			groups = append(groups, Group{
				ID:        fmt.Sprintf("group:%s", aclEl.Value),
				Name:      aclEl.Value,
				Members:   sg.MemberUserIDs,
				Syncables: syncables,
			})
		}
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

func (m *Mattermost) UnremovableUserIDs(ctx context.Context) ([]string, error) {
	out := stringset.New()

	// For Mattermost, just use 'is system admin' as a proxy for 'should not be automatically removed from channels they shouldn't be in'.
	// Note that if someone is promoted to a system admin at the same time as they would be removed from channels, then they will be removed in _that_ sync run.
	if m.SystemAdminGroup != "" {
		sg, ok, err := m.GroupStore.LoadGroup(ctx, m.SystemAdminGroup)
		if err != nil {
			return nil, fmt.Errorf("failed to load SystemAdminGroup %q: %w", m.SystemAdminGroup, err)
		} else if !ok {
			return nil, fmt.Errorf("SystemAdminGroup set to %q but no group by that name in the KV store - does it actually exist?", m.SystemAdminGroup)
		}

		out.Add(sg.MemberUserIDs...)
	}

	// Exempt bots from being removed from things too.
	bots, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]*model.Bot, error) {
		bots, appErr := m.API.GetBots(&model.BotGetOptions{
			Page:    page,
			PerPage: perPage,
		})
		if appErr != nil {
			return nil, appErr
		}
		return bots, nil
	})
	if err != nil {
		return nil, err
	}
	for _, rm := range bots {
		out.Add(rm.UserId)
	}

	return out.Sorted(), nil
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
