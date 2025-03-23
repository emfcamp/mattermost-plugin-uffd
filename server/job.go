package main

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"

	"github.com/mattermost/mattermost/server/public/model"
)

func (p *Plugin) runSyncJob() {
	p.runSync("schedule")
}

const groupSource = "plugin_uffd"

func (p *Plugin) syncUsers(ctx context.Context, trigger string) (map[string]string, error) {
	// Fetch users from UFFD
	uffdUsers, err := p.uffd.GetUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching users from UFFD: %w", err)
	}
	p.API.LogInfo("Got users from UFFD", "trigger", trigger, "groupCount", len(uffdUsers))

	// Index by username
	uffdUsersByUsername := map[string]UffdUser{}
	for _, uffdUser := range uffdUsers {
		uffdUsersByUsername[uffdUser.LoginName] = uffdUser
	}

	// Fetch all users from Mattermost...
	page := 0
	uffdUsernameToMattermostID := map[string]string{}
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
			// For the moment, use the username instead :(
			uffdUser, ok := uffdUsersByUsername[mmUser.Username]
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
			uffdUsernameToMattermostID[uffdUser.LoginName] = mmUser.Id

			// TODO(lukegb): consider updating Mattermost user props based on Uffd data? Although it might be better to just let people change this in Mattermost.
		}

		if len(mmUsers) < perPage {
			break
		}
		page++
	}

	// Create any missing users.
	for _, uffdUser := range uffdUsers {
		if _, ok := uffdUsernameToMattermostID[uffdUser.LoginName]; ok {
			continue // Already got a mattermost ID for them.
		}

		loginID := strconv.Itoa(uffdUser.ID)

		mmUser, appErr := p.API.CreateUser(&model.User{
			Username:            uffdUser.LoginName,
			AuthService:         "openid",
			AuthData:            &loginID,
			Nickname:            uffdUser.DisplayName,
			Email:               uffdUser.Email,
			EmailVerified:       true,
			DisableWelcomeEmail: true,
		})
		p.API.LogInfo("Created user from uffd data", "user", mmUser, "uffdUser", uffdUser, "err", appErr)
		if appErr == nil {
			uffdUsernameToMattermostID[uffdUser.LoginName] = mmUser.Id
		}
	}

	return uffdUsernameToMattermostID, nil
}

func (p *Plugin) syncGroups(ctx context.Context, trigger string, uffdUsernameToMattermostID map[string]string) error {
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

		var currentMemberIDs []string
		page := 0
		for {
			const perPage = 50
			mmUsers, appErr := p.API.GetGroupMemberUsers(mmGroup.Id, page, perPage)
			if appErr != nil {
				return fmt.Errorf("getting members of group %v: %w", mmGroup.Id, err)
			}
			for _, mmUser := range mmUsers {
				currentMemberIDs = append(currentMemberIDs, mmUser.Id)
			}
			if len(mmUsers) < perPage {
				break
			}
		}

		// Sync the user list.
		var wantMemberIDs []string
		for _, u := range g.Members {
			mmUserID, ok := uffdUsernameToMattermostID[u]
			if !ok {
				// This user doesn't have a corresponding Mattermost account.
				// Skip them for now.
				unknownUsers[u] = true
				continue
			}
			wantMemberIDs = append(wantMemberIDs, mmUserID)
		}
		slices.Sort(wantMemberIDs)
		slices.Sort(currentMemberIDs)
		if slices.Equal(wantMemberIDs, currentMemberIDs) {
			p.API.LogDebug("Group already in sync", "groupName", g.Name, "wantMemberIDs", wantMemberIDs, "gotMemberIDs", currentMemberIDs)
			opCount.InSync++
			continue
		}

		p.API.LogInfo("Updating membership of group", "groupName", g.Name, "wantMemberIDs", wantMemberIDs, "gotMemberIDs", currentMemberIDs)
		_, appErr := p.API.UpsertGroupMembers(mmGroup.Id, wantMemberIDs)
		if appErr != nil {
			return fmt.Errorf("updating group %v: %w", g.Name, err)
		}
		opCount.Updated++
	}
	// Create any missing groups.
	for _, g := range uffdGroupMap {
		if _, ok := mmGroupsMap[g.Name]; ok {
			// We saw a Mattermost group for this already.
			continue
		}

		var wantMemberIDs []string
		for _, u := range g.Members {
			mmUserID, ok := uffdUsernameToMattermostID[u]
			if !ok {
				// This user doesn't have a corresponding Mattermost account.
				// Skip them for now.
				unknownUsers[u] = true
				continue
			}
			wantMemberIDs = append(wantMemberIDs, mmUserID)
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

	return nil
}

func (p *Plugin) runSync(trigger string) error {
	p.API.LogDebug("Acquiring UFFD sync mutex", "trigger", trigger)
	p.syncMutex.Lock()
	defer p.syncMutex.Unlock()

	ctx := context.Background()

	p.API.LogInfo("Performing UFFD sync", "trigger", trigger)
	defer p.API.LogInfo("UFFD sync complete", "trigger", trigger)
	uffdUsernameToMattermostID, userErr := p.syncUsers(ctx, trigger)
	if userErr != nil {
		p.API.LogError("Syncing UFFD users failed", "trigger", trigger, "err", userErr)
		return fmt.Errorf("syncing users from UFFD: %w", userErr)
	}
	if grpErr := p.syncGroups(ctx, trigger, uffdUsernameToMattermostID); grpErr != nil {
		p.API.LogError("Syncing UFFD groups failed", "trigger", trigger, "err", grpErr)
		return fmt.Errorf("syncing groups from UFFD: %w", grpErr)
	}
	return nil
}
