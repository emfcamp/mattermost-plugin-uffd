package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) runSyncJob() {
	p.runSync("schedule")
}

const groupSource = "plugin_uffd"

func (p *Plugin) syncUsers(ctx context.Context, trigger string) (map[string]*model.User, error) {
	// Fetch users from UFFD
	uffdUsers, err := p.uffd.GetUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching users from UFFD: %w", err)
	}
	p.API.LogInfo("Got users from UFFD", "trigger", trigger, "groupCount", len(uffdUsers))

	// Index by username and UID
	uffdUsersByUID := map[string]*UffdUser{}
	uffdUsersByUsername := map[string]*UffdUser{}
	for _, uffdUser := range uffdUsers {
		uffdUser := uffdUser
		uffdUsersByUsername[uffdUser.LoginName] = &uffdUser
		uffdUsersByUID[strconv.Itoa(uffdUser.ID)] = &uffdUser
	}

	// Fetch all users from Mattermost...
	page := 0
	uffdUsernameToMattermost := map[string]*model.User{}
	for {
		const perPage = 50
		mmUsers, appErr := p.API.GetUsers(&model.UserGetOptions{
			Page:    page,
			PerPage: perPage,
		})
		if appErr != nil {
			return nil, fmt.Errorf("fetching users page %d from Mattermost: %w", page, err)
		}

		for _, mmUser := range mmUsers {
			if mmUser.AuthService != "openid" {
				continue // ignore non-openid users
			}
			// TODO(lukegb): AuthData doesn't get populated in the response to us - it should contain the uffd ID
			var uffdUser *UffdUser
			var ok bool
			if mmUser.Props != nil && mmUser.Props["uffd/userid"] != "" {
				// If we created the account, it'll have this prop.
				uffdUser, ok = uffdUsersByUID[mmUser.Props["uffd/userid"]]
			}
			if !ok {
				// If not, try matching by username.
				uffdUser, ok = uffdUsersByUsername[mmUser.Username]
			}
			if !ok {
				// User not in result from Uffd; disable them.
				appErr := p.API.UpdateUserActive(mmUser.Id, false)
				p.API.LogInfo("Disabling user that is no longer known to uffd", "username", mmUser.Username, "userID", mmUser.Id, "err", appErr)
				continue
			}
			if mmUser.DeleteAt != 0 {
				appErr := p.API.UpdateUserActive(mmUser.Id, true)
				p.API.LogInfo("Reenabling user that is now known to uffd", "username", mmUser.Username, "userID", mmUser.Id, "err", appErr)
			}
			uffdUsernameToMattermost[uffdUser.LoginName] = mmUser

			// TODO(lukegb): consider updating Mattermost user props based on Uffd data? Although it might be better to just let people change this in Mattermost.
		}

		if len(mmUsers) < perPage {
			break
		}
		page++
	}

	for _, uffdUser := range uffdUsers {
		if mmUser, ok := uffdUsernameToMattermost[uffdUser.LoginName]; ok {
			// Do we need to update them in Mattermost?
			updatedUserAttrs := false
			if mmUser.Props == nil {
				mmUser.Props = map[string]string{}
			}
			if wantUserID := strconv.Itoa(uffdUser.ID); mmUser.Props["uffd/userid"] != wantUserID {
				mmUser.Props["uffd/userid"] = wantUserID
				updatedUserAttrs = true
			}
			if mmUser.Props["uffd/username"] != uffdUser.LoginName {
				mmUser.Props["uffd/username"] = uffdUser.LoginName
				updatedUserAttrs = true
			}
			if mmUser.Username != uffdUser.LoginName {
				mmUser.Username = uffdUser.LoginName
				updatedUserAttrs = true
			}
			if mmUser.Email != uffdUser.Email {
				mmUser.Email = uffdUser.Email
				updatedUserAttrs = true
			}

			if updatedUserAttrs {
				_, appErr := p.API.UpdateUser(mmUser)
				if appErr != nil {
					p.API.LogError("Failed to resync user attributes", "user", mmUser, "err", appErr)
				} else {
					p.API.LogInfo("Resynced user attributes", "user", mmUser)
				}
			}

			continue
		}

		// Missing user, create them:
		loginID := strconv.Itoa(uffdUser.ID)

		mmUser, appErr := p.API.CreateUser(&model.User{
			Username:            uffdUser.LoginName,
			AuthService:         "openid",
			AuthData:            &loginID,
			Nickname:            uffdUser.DisplayName,
			Email:               uffdUser.Email,
			EmailVerified:       true,
			DisableWelcomeEmail: true,
			Props: map[string]string{
				"uffd/userid":   loginID,
				"uffd/username": uffdUser.LoginName,
			},
		})
		p.API.LogInfo("Created user from uffd data", "user", mmUser, "uffdUser", uffdUser, "err", appErr)
		if appErr == nil {
			uffdUsernameToMattermost[uffdUser.LoginName] = mmUser
		}
	}

	return uffdUsernameToMattermost, nil
}

func (p *Plugin) syncGroups(ctx context.Context, trigger string, uffdUsernameToMattermost map[string]*model.User) error {
	cfg := p.getConfiguration()

	// Fetch groups from UFFD
	uffdGroups, err := p.uffd.GetGroups(ctx)
	if err != nil {
		return fmt.Errorf("fetching groups from UFFD: %w", err)
	}
	p.API.LogInfo("Got groups from UFFD", "trigger", trigger, "groupCount", len(uffdGroups))

	// Filter groups to match regex
	if cfg.SyncGroupRegex != "" {
		groupNameRe, err := regexp.Compile(`^` + cfg.SyncGroupRegex + `$`)
		if err != nil {
			return fmt.Errorf("invalid group regex %v: %w", cfg.SyncGroupRegex, err)
		}
		uffdGroups = slices.DeleteFunc(uffdGroups, func(n UffdGroup) bool {
			return !groupNameRe.MatchString(n.Name)
		})
		p.API.LogInfo("Filtered groups from UFFD", "trigger", trigger, "groupCount", len(uffdGroups))
	} else {
		p.API.LogDebug("Using unfiltered group list from UFFD", "trigger", trigger)
	}

	// Fetch all relevant users from Mattermost, and index Uffd groups by name.
	uffdGroupMap := map[string]UffdGroup{}
	for _, g := range uffdGroups {
		uffdGroupMap[g.Name] = g
	}

	// Note that this code doesn't try to be particularly efficient.
	// It loads all the groups from Mattermost and from uffd for diffing.

	// Update groups inside Mattermost.
	mmGroups, appErr := p.API.GetGroupsBySource(groupSource)
	if appErr != nil {
		return fmt.Errorf("getting groups known to Mattermost: %w", err)
	}
	mmGroupsMap := map[string]*model.Group{}
	unknownUsers := map[string]bool{}
	opCount := struct {
		Added   int
		Updated int
		Deleted int
		InSync  int
	}{}
	for _, mmGroup := range mmGroups {
		mmGroupsMap[mmGroup.GetRemoteId()] = mmGroup

		// Has this been deleted from UFFD?
		g, ok := uffdGroupMap[mmGroup.GetRemoteId()]
		if !ok {
			// Not known to UFFD: delete it.
			p.API.DeleteGroup(strconv.Itoa(g.ID))
			p.API.LogInfo("Deleted group no longer known to UFFD", "trigger", trigger, "groupName", mmGroup.GetName(), "groupID", mmGroup.Id)
			opCount.Deleted++
			continue
		}

		currentMemberIDs := stringset.StringSet{}
		page := 0
		for {
			const perPage = 50
			mmUsers, appErr := p.API.GetGroupMemberUsers(mmGroup.Id, page, perPage)
			if appErr != nil {
				return fmt.Errorf("getting members of group %v: %w", mmGroup.Id, err)
			}
			for _, mmUser := range mmUsers {
				currentMemberIDs.Add(mmUser.Id)
			}
			if len(mmUsers) < perPage {
				break
			}
		}

		// Sync the user list.
		wantMemberIDs := stringset.StringSet{}
		for _, u := range g.Members {
			mmUser, ok := uffdUsernameToMattermost[u]
			if !ok {
				// This user doesn't have a corresponding Mattermost account.
				// Skip them for now.
				unknownUsers[u] = true
				continue
			}
			wantMemberIDs.Add(mmUser.Id)
		}
		if wantMemberIDs.Equal(currentMemberIDs) {
			p.API.LogDebug("Group already in sync", "groupName", g.Name)
			opCount.InSync++
			continue
		}
		addMemberIDs := wantMemberIDs.Difference(currentMemberIDs)
		removeMemberIDs := currentMemberIDs.Difference(wantMemberIDs)

		if len(addMemberIDs) > 0 {
			p.API.LogInfo("Adding members to group", "groupName", g.Name, "addMemberIDs", addMemberIDs)

			_, appErr := p.API.UpsertGroupMembers(mmGroup.Id, addMemberIDs.Sorted())
			if appErr != nil {
				return fmt.Errorf("adding group members %v: %w", g.Name, err)
			}
		}
		if len(removeMemberIDs) > 0 {
			p.API.LogInfo("Removing members from group", "groupName", g.Name, "removeMemberIDs", removeMemberIDs)

			for _, memberID := range removeMemberIDs.Sorted() {
				if _, appErr := p.API.DeleteGroupMember(mmGroup.Id, memberID); appErr != nil {
					return fmt.Errorf("deleting group member %v from %v: %w", memberID, mmGroup.Id, appErr)
				}
			}
		}
		if len(addMemberIDs) > 0 || len(removeMemberIDs) > 0 {
			opCount.Updated++
		}
	}
	// Create any missing groups.
	for _, g := range uffdGroupMap {
		if _, ok := mmGroupsMap[g.Name]; ok {
			// We saw a Mattermost group for this already.
			continue
		}

		var wantMemberIDs []string
		for _, u := range g.Members {
			mmUser, ok := uffdUsernameToMattermost[u]
			if !ok {
				// This user doesn't have a corresponding Mattermost account.
				// Skip them for now.
				unknownUsers[u] = true
				continue
			}
			wantMemberIDs = append(wantMemberIDs, mmUser.Id)
		}
		slices.Sort(wantMemberIDs)
		mmGroup := &model.Group{
			Name:        &g.Name,
			DisplayName: g.Name,
			Description: fmt.Sprintf("uffd group %q - ID %d", g.Name, g.ID),
			Source:      groupSource,
			RemoteId:    &g.Name,
			MemberIDs:   wantMemberIDs,
		}
		_, appErr := p.API.CreateGroup(mmGroup)
		if appErr != nil {
			return fmt.Errorf("creating group %v: %w", g.Name, err)
		}
		p.API.LogInfo("Created group", "groupName", g.Name)
	}
	p.API.LogInfo("UFFD sync result", "trigger", trigger, "opCount", opCount, "unknownUsers", slices.Sorted(maps.Keys(unknownUsers)))

	// HACK: changing groups doesn't update their syncables - so users don't get added to their channels/teams!
	// Work around this by manually resyncing the membership lists. What a pain.

	resyncTeams := map[string][]*model.GroupSyncable{}
	resyncChannels := map[string][]*model.GroupSyncable{}
	page := 0
	const perPage = 100
	for {
		groups, appErr := p.API.GetGroups(page, perPage, model.GroupSearchOpts{
			OnlySyncableSources: true,
		}, nil)
		if appErr != nil {
			return appErr
		}
		for _, group := range groups {
			for _, syncableType := range []model.GroupSyncableType{
				model.GroupSyncableTypeTeam,
				model.GroupSyncableTypeChannel,
			} {
				syncables, appErr := p.API.GetGroupSyncables(group.Id, syncableType)
				if appErr != nil {
					p.API.LogError("Fetching group syncables failed", "err", appErr, "group", group, "syncableType", syncableType)
					continue
				}
				for _, syncable := range syncables {
					switch syncableType {
					case model.GroupSyncableTypeTeam:
						resyncTeams[syncable.SyncableId] = append(resyncTeams[syncable.SyncableId], syncable)
					case model.GroupSyncableTypeChannel:
						resyncChannels[syncable.SyncableId] = append(resyncChannels[syncable.SyncableId], syncable)
					}
				}
			}
		}
		page++
		if len(groups) < perPage {
			break
		}
	}

	err = nil
	for teamID, syncables := range resyncTeams {
		if teamErr := p.resyncTeam(teamID, syncables); teamErr != nil {
			err = errors.Join(err, teamErr)
		}
	}
	for channelID, syncables := range resyncChannels {
		if channelErr := p.resyncChannel(channelID, syncables); channelErr != nil {
			err = errors.Join(err, channelErr)
		}
	}
	return err
}

type partitionedSet[F comparable] map[F]stringset.StringSet

func (p partitionedSet[F]) Merge(p2 partitionedSet[F]) {
	for part, vs := range p {
		if vs2, ok := p2[part]; ok {
			p[part] = vs.Union(vs2)
		}
	}
	for part, vs := range p2 {
		if _, ok := p[part]; !ok {
			p[part] = vs.Union(stringset.StringSet{})
		}
	}
}

func (p partitionedSet[F]) Diff(p2 partitionedSet[F]) (inLeftOnly, inRightOnly, inBoth partitionedSet[F]) {
	inLeftOnly = partitionedSet[F]{}
	inRightOnly = partitionedSet[F]{}
	inBoth = partitionedSet[F]{}

	allParts := map[F]struct{}{}
	for part := range p {
		allParts[part] = struct{}{}
	}
	for part := range p2 {
		allParts[part] = struct{}{}
	}

	for part := range allParts {
		inLeftOnly[part] = p[part].Difference(p2[part])
		inRightOnly[part] = p2[part].Difference(p[part])
		inBoth[part] = p[part].Intersection(p2[part])
	}
	return inLeftOnly, inRightOnly, inBoth
}

func (p partitionedSet[F]) Unpartition() stringset.StringSet {
	ss := stringset.StringSet{}
	for _, vs := range p {
		ss.Add(vs.Sorted()...)
	}
	return ss
}

func getMemberIDs[E any, F comparable](thingyID string, call func(string, int, int) ([]E, *model.AppError), getData func(E) (string, F)) (partitionedSet[F], error) {
	parts := map[F]stringset.StringSet{}

	const perPage = 100
	page := 0
	for {
		members, appErr := call(thingyID, page, perPage)
		if appErr != nil {
			return nil, fmt.Errorf("getting page %d of group %s members: %w", page, thingyID, appErr)
		}
		for _, member := range members {
			id, partID := getData(member)
			part := parts[partID]
			if part == nil {
				parts[partID] = stringset.StringSet{}
				part = parts[partID]
			}
			part.Add(id)
		}
		page++
		if len(members) < perPage {
			break
		}
	}

	return parts, nil
}

func (p *Plugin) resyncTeam(teamID string, syncables []*model.GroupSyncable) error {
	team, appErr := p.API.GetTeam(teamID)
	if appErr != nil {
		return fmt.Errorf("GetTeam(%q): %w", teamID, appErr)
	}
	if !team.IsGroupConstrained() {
		// Doesn't use groups to control membership???
		return nil
	}
	p.API.LogDebug("resyncing team", "team", team, "syncables", syncables)

	wantMembers := partitionedSet[bool]{}
	for _, syncable := range syncables {
		members, err := getMemberIDs(syncable.GroupId, p.API.GetGroupMemberUsers, func(m *model.User) (string, bool) { return m.Id, syncable.SchemeAdmin })
		if err != nil {
			return fmt.Errorf("getting members of group %s: %w", syncable.GroupId, err)
		}
		wantMembers.Merge(members)
	}
	wantMembersUnpartitioned := wantMembers.Unpartition()

	gotMembers, err := getMemberIDs(teamID, p.API.GetTeamMembers, func(m *model.TeamMember) (string, bool) { return m.UserId, m.SchemeAdmin })
	if err != nil {
		return fmt.Errorf("getting members of team %s: %w", teamID, err)
	}
	gotMembersUnpartitioned := gotMembers.Unpartition()

	toAdd := wantMembersUnpartitioned.Difference(gotMembersUnpartitioned)
	toRemove := gotMembersUnpartitioned.Difference(wantMembersUnpartitioned)

	p.API.LogDebug("XXX want/got", "got", gotMembers, "want", wantMembers)

	mutateTeamMember := func(tm *model.TeamMember) error {
		roles := stringset.FromSlice(tm.GetRoles())
		mutatedRoles := false
		if wantMembers[true].Contains(tm.UserId) == tm.SchemeAdmin {
			// Check wantMembers[true] first - both lists might contain the member, and we want being an admin to win.
			return nil
		}
		if wantMembers[true].Contains(tm.UserId) && !tm.SchemeAdmin {
			roles.Add("team_admin")
			mutatedRoles = true
		} else if wantMembers[false].Contains(tm.UserId) && tm.SchemeAdmin {
			roles.Remove("team_admin")
			mutatedRoles = true
		}
		if !mutatedRoles {
			return nil
		}

		rolesStr := strings.Join(roles.Sorted(), " ")
		if _, appErr := p.API.UpdateTeamMemberRoles(teamID, tm.UserId, rolesStr); appErr != nil {
			return fmt.Errorf("updating team member roles for %v in team %v to %v: %w", tm.UserId, teamID, rolesStr, appErr)
		}
		return nil
	}

	if len(toAdd) > 0 {
		tms, appErr := p.API.CreateTeamMembers(teamID, toAdd.Sorted(), "" /* no requestor */)
		if appErr != nil {
			return fmt.Errorf("creating team members: %w", err)
		}

		for _, tm := range tms {
			if err := mutateTeamMember(tm); err != nil {
				return err
			}
		}
	}
	if len(toRemove) > 0 {
		for _, userID := range toRemove.Sorted() {
			if appErr := p.API.DeleteTeamMember(teamID, userID, "" /* no requestor */); appErr != nil {
				return fmt.Errorf("removing %v from team %v: %w", userID, teamID, err)
			}
		}
	}

	mutateTeamMemberByUserID := func(userID string) error {
		tm, err := p.API.GetTeamMember(teamID, userID)
		if err != nil {
			return fmt.Errorf("fetching TeamMember for team %v user %v: %w", teamID, userID, err)
		}
		return mutateTeamMember(tm)
	}
	for promoteUserID := range wantMembers[true].Difference(gotMembers[true]).Intersection(gotMembers[false]) {
		if err := mutateTeamMemberByUserID(promoteUserID); err != nil {
			return err
		}
	}
	for demoteUserID := range wantMembers[false].Difference(gotMembers[false]).Intersection(gotMembers[true]) {
		if err := mutateTeamMemberByUserID(demoteUserID); err != nil {
			return err
		}
	}

	return nil
}

func (p *Plugin) resyncChannel(channelID string, syncables []*model.GroupSyncable) error {
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return fmt.Errorf("GetChannel(%q): %w", channelID, appErr)
	}
	if !channel.IsGroupConstrained() {
		// Doesn't use groups to control membership???
		return nil
	}
	p.API.LogDebug("resyncing channel", "channel", channel, "syncables", syncables)

	wantMembers := partitionedSet[bool]{}
	for _, syncable := range syncables {
		members, err := getMemberIDs(syncable.GroupId, p.API.GetGroupMemberUsers, func(m *model.User) (string, bool) { return m.Id, syncable.SchemeAdmin })
		if err != nil {
			return fmt.Errorf("getting members of group %s: %w", syncable.GroupId, err)
		}
		wantMembers.Merge(members)
	}
	wantMembersUnpartitioned := wantMembers.Unpartition()

	gotMembers, err := getMemberIDs(channelID, func(channelID string, pageNum, perPage int) ([]model.ChannelMember, *model.AppError) {
		ms, err := p.API.GetChannelMembers(channelID, pageNum, perPage)
		return []model.ChannelMember(ms), err
	}, func(m model.ChannelMember) (string, bool) { return m.UserId, m.SchemeAdmin })
	if err != nil {
		return fmt.Errorf("getting members of channel %s: %w", channelID, err)
	}
	gotMembersUnpartitioned := gotMembers.Unpartition()

	toAdd := wantMembersUnpartitioned.Difference(gotMembersUnpartitioned)
	toRemove := gotMembersUnpartitioned.Difference(wantMembersUnpartitioned)

	mutateChannelMember := func(cm *model.ChannelMember) error {
		roles := stringset.FromSlice(cm.GetRoles())
		mutatedRoles := false
		if wantMembers[true].Contains(cm.UserId) == cm.SchemeAdmin {
			// Check wantMembers[true] first - both lists might contain the member, and we want being an admin to win.
			return nil
		}
		if wantMembers[true].Contains(cm.UserId) && !cm.SchemeAdmin {
			roles.Add("channel_admin")
			mutatedRoles = true
		} else if wantMembers[false].Contains(cm.UserId) && cm.SchemeAdmin {
			roles.Remove("channel_admin")
			mutatedRoles = true
		}
		if !mutatedRoles {
			return nil
		}

		rolesStr := strings.Join(roles.Sorted(), " ")
		if _, appErr := p.API.UpdateChannelMemberRoles(channelID, cm.UserId, rolesStr); appErr != nil {
			return fmt.Errorf("updating channel member roles for %v in channel %v to %v: %w", cm.UserId, channelID, rolesStr, appErr)
		}
		return nil
	}

	if len(toAdd) > 0 {
		for _, userID := range toAdd.Sorted() {
			cm, appErr := p.API.AddUserToChannel(channelID, userID, "" /* no requestor */)
			if appErr != nil {
				return fmt.Errorf("creating channel members: %w", err)
			}
			if err := mutateChannelMember(cm); err != nil {
				return err
			}
		}
	}
	if len(toRemove) > 0 {
		for _, userID := range toRemove.Sorted() {
			if appErr := p.API.DeleteChannelMember(channelID, userID); appErr != nil {
				return fmt.Errorf("removing %v from channel %v: %w", userID, channelID, err)
			}
		}
	}

	mutateChannelMemberByUserID := func(userID string) error {
		tm, err := p.API.GetChannelMember(channelID, userID)
		if err != nil {
			return fmt.Errorf("fetching ChannelMember for channel %v user %v: %w", channelID, userID, err)
		}
		return mutateChannelMember(tm)
	}
	for promoteUserID := range wantMembers[true].Difference(gotMembers[true]).Intersection(gotMembers[false]) {
		if err := mutateChannelMemberByUserID(promoteUserID); err != nil {
			return err
		}
	}
	for demoteUserID := range wantMembers[false].Difference(gotMembers[false]).Intersection(gotMembers[true]) {
		if err := mutateChannelMemberByUserID(demoteUserID); err != nil {
			return err
		}
	}

	return nil
}

func (p *Plugin) runSync(trigger string) error {
	p.API.LogDebug("Acquiring UFFD sync mutex", "trigger", trigger)
	p.syncMutex.Lock()
	defer p.syncMutex.Unlock()

	ctx := context.Background()

	p.API.LogInfo("Performing UFFD sync", "trigger", trigger)
	defer p.API.LogInfo("UFFD sync complete", "trigger", trigger)
	uffdUsernameToMattermost, userErr := p.syncUsers(ctx, trigger)
	if userErr != nil {
		p.API.LogError("Syncing UFFD users failed", "trigger", trigger, "err", userErr)
		return fmt.Errorf("syncing users from UFFD: %w", userErr)
	}
	if grpErr := p.syncGroups(ctx, trigger, uffdUsernameToMattermost); grpErr != nil {
		p.API.LogError("Syncing UFFD groups failed", "trigger", trigger, "err", grpErr)
		return fmt.Errorf("syncing groups from UFFD: %w", grpErr)
	}
	return nil
}
