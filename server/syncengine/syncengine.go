// Package syncengine contains the syncing logic of users/groups -> a service.
//
// It does not handle resyncing syncables.
package syncengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

type SyncEngine struct {
	IDP     IDPAPI
	Service ServiceAPI
}

type Membership struct {
	GroupID string
	UserID  string
}

type Outcome struct {
	CreatedUsers []*User[string]
	UpdatedUsers []*User[string]

	CreatedGroups []*Group[string]
	UpdatedGroups []*Group[string]
	DeletedGroups []*Group[string]

	CreatedMemberships []Membership
	RemovedMemberships []Membership
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
	idpGroups, err := s.IDP.FetchGroups(ctx)
	if err != nil {
		return fmt.Errorf("fetching groups from IdP: %w", err)
	}

	serviceGroups, err := s.Service.FetchGroups(ctx)
	if err != nil {
		return fmt.Errorf("fetching groups from service: %w", err)
	}

	idpGroupsByName := map[string]*Group[int]{}
	for _, idpGroup := range idpGroups {
		idpGroupsByName[idpGroup.Name] = idpGroup
	}
	serviceGroupsByName := map[string]*Group[string]{}
	for _, serviceGroup := range serviceGroups {
		serviceGroupsByName[serviceGroup.Name] = serviceGroup
	}
	serviceUsersByIDPID, err := c.getServiceUsersByIDPID(ctx, s)
	if err != nil {
		return fmt.Errorf("fetching user<->ID mapping: %w", err)
	}

	var groupsToCreate []*Group[int]
	var groupsToDelete []*Group[string]
	var groupsToUpdate []*Group[string]

	for _, idpGroup := range idpGroups {
		serviceGroup, ok := serviceGroupsByName[idpGroup.Name]
		if !ok {
			groupsToCreate = append(groupsToCreate, idpGroup)
		} else {
			groupsToUpdate = append(groupsToUpdate, serviceGroup)
		}
	}
	for _, serviceGroup := range serviceGroups {
		if _, ok := idpGroupsByName[serviceGroup.Name]; !ok {
			groupsToDelete = append(groupsToDelete, serviceGroup)
		}
	}

	var mergedErr error
	if len(groupsToCreate) > 0 {
		err := func() error {
			createdGroups, err := s.Service.CreateGroups(ctx, groupsToCreate)
			if err != nil {
				return err
			}
			out.CreatedGroups = append(out.CreatedGroups, createdGroups...)

			for n, createdGroup := range createdGroups {
				originalGroup := groupsToCreate[n]
				members := make([]string, 0, len(originalGroup.MemberUserIDs))
				for _, idpID := range originalGroup.MemberUserIDs {
					serviceUser, ok := serviceUsersByIDPID[idpID]
					if ok {
						members = append(members, serviceUser.UserID)
					}
				}
				if err := s.Service.AddGroupMembers(ctx, createdGroup.GroupID.(string), members); err != nil {
					return fmt.Errorf("adding group members %v to %v: %w", members, createdGroup.GroupID, err)
				}
				for _, m := range members {
					out.CreatedMemberships = append(out.CreatedMemberships, Membership{
						GroupID: createdGroup.GroupID.(string),
						UserID:  m,
					})
					createdGroup.MemberUserIDs = append(createdGroup.MemberUserIDs, m)
				}
			}
			return nil
		}()
		if err != nil {
			mergedErr = errors.Join(mergedErr, err)
		}
	}
	if len(groupsToUpdate) > 0 {
		updatedGroups, didUpdate, err := s.Service.UpdateGroups(ctx, groupsToUpdate)
		if err != nil {
			mergedErr = errors.Join(mergedErr, err)
		}

		for n, serviceGroup := range updatedGroups {
			currentMembers := stringset.FromSlice(serviceGroup.MemberUserIDs)

			idpGroup := idpGroupsByName[serviceGroup.Name]
			wantMembers := stringset.New()
			for _, idpMember := range idpGroup.MemberUserIDs {
				serviceUser, ok := serviceUsersByIDPID[idpMember]
				if ok {
					wantMembers.Add(serviceUser.UserID)
				}
			}

			updatedThisGroup := didUpdate[n]
			membersToAdd := wantMembers.Difference(currentMembers).Sorted()
			if len(membersToAdd) > 0 {
				if err := s.Service.AddGroupMembers(ctx, serviceGroup.GroupID.(string), membersToAdd); err != nil {
					mergedErr = errors.Join(mergedErr, fmt.Errorf("adding group members %v to group %v: %w", membersToAdd, serviceGroup.GroupID, err))
				}
				for _, m := range membersToAdd {
					out.CreatedMemberships = append(out.CreatedMemberships, Membership{
						GroupID: serviceGroup.GroupID.(string),
						UserID:  m,
					})
				}
				updatedThisGroup = true
			}
			membersToRemove := currentMembers.Difference(wantMembers).Sorted()
			if len(membersToRemove) > 0 {
				if err := s.Service.RemoveGroupMembers(ctx, serviceGroup.GroupID.(string), membersToRemove); err != nil {
					mergedErr = errors.Join(mergedErr, fmt.Errorf("removing group members from group %v: %w", serviceGroup.GroupID, err))
				}
				for _, m := range membersToRemove {
					out.RemovedMemberships = append(out.RemovedMemberships, Membership{
						GroupID: serviceGroup.GroupID.(string),
						UserID:  m,
					})
				}
				updatedThisGroup = true
			}
			serviceGroup.MemberUserIDs = wantMembers.Sorted()
			if updatedThisGroup {
				out.UpdatedGroups = append(out.UpdatedGroups, serviceGroup)
			}
		}
	}
	if len(groupsToDelete) > 0 {
		if err := s.Service.DeleteGroups(ctx, groupsToDelete); err != nil {
			mergedErr = errors.Join(mergedErr, err)
		}
		out.DeletedGroups = append(out.DeletedGroups, groupsToDelete...)
	}
	return mergedErr
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
