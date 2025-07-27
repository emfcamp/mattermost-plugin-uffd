package syncengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
)

const (
	MMPluginSource model.GroupSource = "plugin_uffd"
)

// MattermostPluginGroupBackend uses Mattermost's plugin API to manipulate groups.
//
// This requires an Enterprise license (or at least a license with the LDAP Groups feature enabled).
type MattermostPluginGroupBackend struct {
	API mattermostPluginAPI
}

var _ MattermostGroupBackend = ((*MattermostPluginGroupBackend)(nil))

func mattermostPluginGroupToSyncGroup(ctx context.Context, s *MattermostPluginGroupBackend, serviceGroup *model.Group) (*Group[string], error) {
	groupMembers, err := paginator.FetchPaginated(mmDefaultPageSize, func(page, perPage int) ([]string, error) {
		us, err := s.API.GetGroupMemberUsers(serviceGroup.Id, page, perPage)
		if err != nil {
			return nil, err
		}
		userIDs := make([]string, len(us))
		for n, u := range us {
			userIDs[n] = u.Id
		}
		return userIDs, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching group membership for group %v from Mattermost: %w", serviceGroup.Id, err)
	}

	return &Group[string]{
		GroupID:       serviceGroup.Id,
		Name:          serviceGroup.GetName(),
		IDPID:         serviceGroup.GetRemoteId(),
		MemberUserIDs: groupMembers,

		ServiceGroup: serviceGroup,
	}, nil
}

func (s *MattermostPluginGroupBackend) FetchGroups(ctx context.Context) ([]*Group[string], error) {
	serviceGroups, err := s.API.GetGroupsBySource(MMPluginSource)
	if err != nil {
		return nil, fmt.Errorf("fetching groups for source %v from Mattermost: %w", MMPluginSource, err)
	}

	out := make([]*Group[string], 0, len(serviceGroups))
	for _, serviceGroup := range serviceGroups {
		groupMembers, err := paginator.FetchPaginated(mmDefaultPageSize, func(page, perPage int) ([]string, error) {
			us, err := s.API.GetGroupMemberUsers(serviceGroup.Id, page, perPage)
			if err != nil {
				return nil, err
			}
			userIDs := make([]string, len(us))
			for n, u := range us {
				userIDs[n] = u.Id
			}
			return userIDs, nil
		})
		if err != nil {
			return nil, fmt.Errorf("fetching group membership for group %v from Mattermost: %w", serviceGroup.Id, err)
		}

		out = append(out, &Group[string]{
			GroupID:       serviceGroup.Id,
			Name:          serviceGroup.GetName(),
			IDPID:         serviceGroup.GetRemoteId(),
			MemberUserIDs: groupMembers,
			ServiceGroup:  serviceGroup,
		})
	}
	return out, nil
}

func (s *MattermostPluginGroupBackend) CreateGroups(ctx context.Context, groups []*Group[int]) ([]*Group[string], error) {
	l := ctxlog.FromContext(ctx)
	out := make([]*Group[string], len(groups))
	var mergedErr error
	for n, g := range groups {
		remoteID := fmt.Sprintf("%v", g.GroupID)
		serviceGroup, appErr := s.API.CreateGroup(&model.Group{
			Name:        &g.Name,
			DisplayName: g.Name,
			Description: fmt.Sprintf("uffd group %v", g.Name),
			Source:      MMPluginSource,
			RemoteId:    &remoteID,
		})
		if appErr != nil {
			l.WithError(appErr).Errorf("creating group %v", g.Name)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("creating group %v: %w", g.Name, appErr))
			continue
		}

		ng, err := mattermostPluginGroupToSyncGroup(ctx, s, serviceGroup)
		if err != nil {
			l.WithError(err).Errorf("validating created group %v (%v)", g.Name, serviceGroup.Id)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("validating created group %v: %w", g.Name, err))
			continue
		}
		out[n] = ng
	}
	if mergedErr != nil {
		return nil, mergedErr
	}
	return out, nil
}

func (s *MattermostPluginGroupBackend) DeleteGroups(ctx context.Context, groups []*Group[string]) error {
	l := ctxlog.FromContext(ctx)
	var mergedErr error
	for _, group := range groups {
		if _, appErr := s.API.DeleteGroup(group.GroupID.(string)); appErr != nil {
			l.WithError(appErr).Errorf("deleting group %v (%v)", group.Name, group.GroupID)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("deleting group %v: %w", group.Name, appErr))
		}
	}
	return mergedErr
}

func (s *MattermostPluginGroupBackend) AddGroupMembers(ctx context.Context, groupID string, memberIDs []string) error {
	_, appErr := s.API.UpsertGroupMembers(groupID, memberIDs)
	if appErr != nil {
		ctxlog.FromContext(ctx).WithError(appErr).Errorf("adding group members to %v", groupID)
		return appErr
	}
	return nil
}

func (s *MattermostPluginGroupBackend) RemoveGroupMembers(ctx context.Context, groupID string, memberIDs []string) error {
	var mergedErr error
	for _, memberID := range memberIDs {
		_, appErr := s.API.DeleteGroupMember(groupID, memberID)
		if appErr != nil {
			ctxlog.FromContext(ctx).WithError(appErr).Errorf("deleting group member %v from %v", memberID, groupID)
			mergedErr = errors.Join(mergedErr, appErr)
		}
	}
	return mergedErr
}
