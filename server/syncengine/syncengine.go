// Package syncengine contains the syncing logic of users -> a service.
//
// It does not handle resyncing syncables.
package syncengine

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

var (
	teamGroupRegexp = regexp.MustCompile(`^(moderation|team)_(.*)$`)
)

type SyncEngine struct {
	IDP        IDPAPI
	Service    ServiceAPI
	GroupStore DataStoreAPI

	AdditionalGroups []string
}

type Outcome struct {
	CreatedUsers []*User[string]
	UpdatedUsers []*User[string]
}

type Cache struct {
	serviceUsersByIDPID map[int]*User[string]
}

func (c *Cache) getServiceUsersByIDPID(ctx context.Context, s *SyncEngine) (map[int]*User[string], error) {
	if c.serviceUsersByIDPID != nil {
		return c.serviceUsersByIDPID, nil
	}

	serviceUsers, err := s.Service.FetchUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching users from service: %w", err)
	}
	serviceUsersByIDPID := map[int]*User[string]{}
	for _, serviceUser := range serviceUsers {
		serviceUsersByIDPID[serviceUser.IDPUserID] = serviceUser
	}
	c.serviceUsersByIDPID = serviceUsersByIDPID
	return serviceUsersByIDPID, nil
}

func (s *SyncEngine) FullSyncUsers(ctx context.Context, c *Cache, out *Outcome) error {
	idpUsers, err := s.IDP.FetchUsers(ctx)
	if err != nil {
		return fmt.Errorf("fetching users from IdP: %w", err)
	}

	idpUsersByIDPID := map[int]*User[int]{}
	for _, idpUser := range idpUsers {
		idpUsersByIDPID[idpUser.UserID] = idpUser
	}
	serviceUsersByIDPID, err := c.getServiceUsersByIDPID(ctx, s)
	if err != nil {
		return fmt.Errorf("fetching users from service: %w", err)
	}

	var usersToCreate []*User[int]
	var usersToUpdate []*User[string]
	for _, idpUser := range idpUsers {
		serviceUser, ok := serviceUsersByIDPID[idpUser.UserID]
		if !ok {
			// Doesn't exist in service yet.
			usersToCreate = append(usersToCreate, idpUser)
		} else {
			serviceUser.Username = idpUser.Username
			serviceUser.DisplayName = idpUser.DisplayName
			serviceUser.Email = idpUser.Email
			serviceUser.Active = idpUser.Active
			usersToUpdate = append(usersToUpdate, serviceUser)
		}
	}
	for _, serviceUser := range serviceUsersByIDPID {
		if _, ok := idpUsersByIDPID[serviceUser.IDPUserID]; !ok {
			// Not known to the IdP.
			if serviceUser.Active {
				serviceUser.Active = false
				usersToUpdate = append(usersToUpdate, serviceUser)
			}
		}
	}

	var mergedErr error
	if len(usersToCreate) > 0 {
		createdUsers, err := s.Service.CreateUsers(ctx, usersToCreate)
		if err != nil {
			mergedErr = errors.Join(mergedErr, err)
		}
		out.CreatedUsers = append(out.CreatedUsers, createdUsers...)
		for _, u := range createdUsers {
			serviceUsersByIDPID[u.IDPUserID] = u
		}
	}
	if len(usersToUpdate) > 0 {
		updatedUsers, updated, err := s.Service.UpdateUsers(ctx, usersToUpdate)
		if err != nil {
			mergedErr = errors.Join(mergedErr, err)
		}
		for n, didUpdate := range updated {
			if didUpdate {
				out.UpdatedUsers = append(out.UpdatedUsers, updatedUsers[n])
			}
		}
	}
	return mergedErr
}

func (s *SyncEngine) FullSyncGroups(ctx context.Context, c *Cache, out *Outcome) error {
	l := ctxlog.FromContext(ctx)

	idpGroups, err := s.IDP.FetchGroups(ctx)
	if err != nil {
		return err
	}
	serviceUsersByIDPID, err := c.getServiceUsersByIDPID(ctx, s)
	if err != nil {
		return fmt.Errorf("fetching users from service: %w", err)
	}

	serviceGroupFromIDPGroup := func(g *Group[int]) (*Group[string], error) {
		sGroup := &Group[string]{
			GroupID: g.Name,
			Name:    g.Name,
			IDPID:   g.IDPID,
		}
		for _, m := range g.MemberUserIDs {
			serviceUser, ok := serviceUsersByIDPID[m]
			if !ok {
				return nil, fmt.Errorf("missing service user %v (in group %v / %v)", m, g.Name, g.GroupID)
			}
			if !serviceUser.Active {
				// Don't include inactive users in any groups.
				continue
			}
			sGroup.MemberUserIDs = append(sGroup.MemberUserIDs, serviceUser.ServiceUserID)
		}
		return sGroup, nil
	}

	// For anything in AdditionalGroups, save it literally:
	var additionalGroups []*Group[string]
	wantAdditionalGroups := stringset.FromSlice(s.AdditionalGroups)
	for _, idpGroup := range idpGroups {
		if !wantAdditionalGroups.Contains(idpGroup.Name) {
			continue
		}
		sGroup, err := serviceGroupFromIDPGroup(idpGroup)
		if err != nil {
			return err
		}
		additionalGroups = append(additionalGroups, sGroup)
	}
	if err := s.GroupStore.SaveGroups(ctx, additionalGroups); err != nil {
		return fmt.Errorf("saving AdditionalGroups %v: %w", additionalGroups, err)
	}

	// Construct teams based on what groups we got from the IdP:
	type teamDataStruct struct {
		name         string
		leadsGroup   *Group[string]
		membersGroup *Group[string]
	}
	teamDatas := map[string]*teamDataStruct{}
	for _, idpGroup := range idpGroups {
		teamGroupBits := teamGroupRegexp.FindStringSubmatch(idpGroup.Name)
		if len(teamGroupBits) < 3 {
			continue
		}
		teamPart, teamName := teamGroupBits[1], teamGroupBits[2]
		teamData := teamDatas[teamName]
		if teamData == nil {
			teamData = &teamDataStruct{name: teamName}
			teamDatas[teamName] = teamData
		}

		serviceGroup, err := serviceGroupFromIDPGroup(idpGroup)
		if err != nil {
			return err
		}
		switch teamPart {
		case "moderation":
			teamData.leadsGroup = serviceGroup
		case "team":
			teamData.membersGroup = serviceGroup
		default:
			return fmt.Errorf("unknown team part %q from %v", teamPart, teamName)
		}
	}
	var teams []Team
	for _, teamData := range teamDatas {
		if teamData.leadsGroup == nil && teamData.membersGroup == nil {
			return fmt.Errorf("team %v somehow missing both a members and leads group?", teamData.name)
		}
		if teamData.leadsGroup == nil {
			l.WithField("team", teamData.name).Warningf("team %s missing a leads group; using members group as leads group", teamData.name)
			teamData.leadsGroup = teamData.membersGroup
		}
		if teamData.membersGroup == nil {
			l.WithField("team", teamData.name).Warningf("team %s missing a members group; using leads group as members group", teamData.name)
			teamData.membersGroup = teamData.leadsGroup
		}
		teams = append(teams, Team{
			Name:    teamData.name,
			Leads:   teamData.leadsGroup.MemberUserIDs,
			Members: teamData.membersGroup.MemberUserIDs,
		})
	}
	if err := s.GroupStore.SaveTeams(ctx, teams); err != nil {
		return fmt.Errorf("saving teams: %w", err)
	}

	return nil
}

func (s *SyncEngine) FullSync(ctx context.Context) (*Outcome, error) {
	c := &Cache{}
	out := &Outcome{}
	if err := s.FullSyncUsers(ctx, c, out); err != nil {
		return out, fmt.Errorf("syncing users: %w", err)
	}
	if err := s.FullSyncGroups(ctx, c, out); err != nil {
		return out, fmt.Errorf("syncing groups: %w", err)
	}
	return out, nil
}
