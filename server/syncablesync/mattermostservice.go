package syncablesync

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
)

const (
	perPageDefault = 1000
)

type Mattermost struct {
	API mattermostAPI

	SystemAdminGroup   string
	SystemManagerGroup string
}

var _ ServiceAPI = ((*Mattermost)(nil))

type mattermostAPI interface {
	mattermostChannelAPI
	mattermostTeamAPI
	mattermostSystemRoleAPI

	GetGroupsBySource(model.GroupSource) ([]*model.Group, *model.AppError)
	GetGroupMemberUsers(groupID string, page, perPage int) ([]*model.User, *model.AppError)
	GetGroupSyncables(groupID string, syncableType model.GroupSyncableType) ([]*model.GroupSyncable, *model.AppError)
}

var _ mattermostAPI = ((plugin.API)(nil))

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
		mattermostSyncableTypeChannel:    &mattermostChannelHandler{m.API},
		mattermostSyncableTypeTeam:       &mattermostTeamHandler{m.API},
	}
}

func (m *Mattermost) FetchGroupsAndSyncables(ctx context.Context) ([]Group, error) {
	groups, appErr := m.API.GetGroupsBySource(syncengine.MMPluginSource)
	if appErr != nil {
		return nil, appErr
	}

	handlers := m.SyncableHandlers()
	var out []Group
	for _, group := range groups {
		// Populate members
		groupMembers, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]string, error) {
			users, appErr := m.API.GetGroupMemberUsers(group.Id, page, perPage)
			if appErr != nil {
				return nil, appErr
			}
			userIDs := make([]string, len(users))
			for n, u := range users {
				userIDs[n] = u.Id
			}
			return userIDs, nil
		})
		if err != nil {
			return nil, fmt.Errorf("fetching group members for %v: %w", group.Id, err)
		}

		// Populate syncables
		var syncables []Syncable
		for _, st := range slices.Sorted(maps.Keys(handlers)) {
			groupSyncables, err := m.API.GetGroupSyncables(group.Id, model.GroupSyncableType(st))
			if err != nil {
				return nil, fmt.Errorf("fetching group %v syncables for %v: %w", st, group.Id, err)
			}
			for _, gs := range groupSyncables {
				syncables = append(syncables, Syncable{
					Target: SyncableTarget{
						Type: st,
						ID:   gs.SyncableId,
					},
					GrantsAdmin: gs.SchemeAdmin,
					ServiceType: gs,
				})
			}
		}

		if m.SystemAdminGroup != "" && group.GetName() == m.SystemAdminGroup {
			syncables = append(syncables, Syncable{
				Target: SyncableTarget{
					Type: mattermostSyncableTypeSystemRole,
					ID:   "system_admin",
				},
			})
		}
		if m.SystemManagerGroup != "" && group.GetName() == m.SystemManagerGroup {
			syncables = append(syncables, Syncable{
				Target: SyncableTarget{
					Type: mattermostSyncableTypeSystemRole,
					ID:   "system_manager",
				},
			})
		}

		out = append(out, Group{
			ID:   group.Id,
			Name: group.GetName(),

			Members:   groupMembers,
			Syncables: syncables,

			ServiceType: group,
		})
	}

	return out, nil
}
