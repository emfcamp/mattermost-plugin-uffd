package syncengine

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	MMPluginSource model.GroupSource = "plugin_uffd"

	MMIdPUserIDProp   = "idp/userid"
	MMIdPUsernameProp = "idp/username"

	mmDefaultPageSize = 1000
)

type mattermostPluginAPI interface {
	GetUsers(*model.UserGetOptions) ([]*model.User, *model.AppError)
	CreateUser(*model.User) (*model.User, *model.AppError)
	GetUser(userID string) (*model.User, *model.AppError)
	UpdateUser(*model.User) (*model.User, *model.AppError)
	UpdateUserActive(userID string, active bool) *model.AppError

	GetGroupsBySource(source model.GroupSource) ([]*model.Group, *model.AppError)
	CreateGroup(*model.Group) (*model.Group, *model.AppError)
	DeleteGroup(groupID string) (*model.Group, *model.AppError)

	GetGroupMemberUsers(groupID string, page, perPage int) ([]*model.User, *model.AppError)
	UpsertGroupMembers(groupID string, memberIDs []string) ([]*model.GroupMember, *model.AppError)
	DeleteGroupMember(groupID, memberID string) (*model.GroupMember, *model.AppError)
}

var _ mattermostPluginAPI = (plugin.API)(nil)

type MattermostService struct {
	API mattermostPluginAPI
}

var _ ServiceAPI = (*MattermostService)(nil)

type strError string

func (s strError) Error() string { return string(s) }

var ErrNotIdPUser strError = "not a user created from the IdP"

func mattermostUserToSyncUser(serviceUser *model.User) (*User[string], error) {
	idpUserIDStr := serviceUser.Props[MMIdPUserIDProp]
	if idpUserIDStr == "" {
		return nil, fmt.Errorf("loading user %v: %w", serviceUser.Id, ErrNotIdPUser)
	}
	idpUserID, err := strconv.Atoi(idpUserIDStr)
	if err != nil {
		return nil, fmt.Errorf("parsing user %v's %v prop (%q): %w", serviceUser.Id, MMIdPUserIDProp, idpUserIDStr, err)
	}

	return &User[string]{
		UserID:      serviceUser.Id,
		Username:    serviceUser.Username,
		DisplayName: serviceUser.Nickname,
		Email:       serviceUser.Email,
		Active:      serviceUser.DeleteAt == 0,

		IdPUserID:     idpUserID,
		ServiceUserID: serviceUser.Id,
		ServiceUser:   serviceUser,
	}, nil
}

func (s *MattermostService) FetchUsers(ctx context.Context) ([]*User[string], error) {
	serviceUsers, err := paginator.FetchPaginated(mmDefaultPageSize, func(page, perPage int) ([]*model.User, error) {
		users, appErr := s.API.GetUsers(&model.UserGetOptions{
			Page:    page,
			PerPage: perPage,
		})
		if appErr != nil {
			return nil, appErr
		}
		return users, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching users from Mattermost: %w", err)
	}
	out := make([]*User[string], 0, len(serviceUsers))
	for _, serviceUser := range serviceUsers {
		u, err := mattermostUserToSyncUser(serviceUser)
		if errors.Is(err, ErrNotIdPUser) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("fetching users from Mattermost: %w", err)
		}
		out = append(out, u)
	}
	return out, nil
}

func mattermostGroupToSyncGroup(ctx context.Context, s *MattermostService, serviceGroup *model.Group) (*Group[string], error) {
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
		IdPID:         serviceGroup.GetRemoteId(),
		MemberUserIDs: groupMembers,

		ServiceGroup: serviceGroup,
	}, nil
}

func (s *MattermostService) FetchGroups(ctx context.Context) ([]*Group[string], error) {
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
			IdPID:         serviceGroup.GetRemoteId(),
			MemberUserIDs: groupMembers,
			ServiceGroup:  serviceGroup,
		})
	}
	return out, nil
}

func (s *MattermostService) CreateUsers(ctx context.Context, users []*User[int]) ([]*User[string], error) {
	l := ctxlog.FromContext(ctx)
	out := make([]*User[string], len(users))
	var mergedErr error
	for n, u := range users {
		authData := strconv.Itoa(u.UserID)
		retUser, appErr := s.API.CreateUser(&model.User{
			Username:            u.Username,
			Nickname:            u.DisplayName,
			Email:               u.Email,
			EmailVerified:       true,
			DisableWelcomeEmail: true,
			AllowMarketing:      false,
			AuthService:         "openid",
			AuthData:            &authData,
			Props: map[string]string{
				MMIdPUserIDProp:   authData,
				MMIdPUsernameProp: u.Username,
			},
		})
		if appErr != nil {
			l.WithError(appErr).Errorf("creating user %v", u.Username)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("creating user %v: %w", u.Username, appErr))
			continue
		}

		if !u.Active {
			if appErr := s.API.UpdateUserActive(retUser.Id, false); appErr != nil {
				l.WithError(appErr).Errorf("deactivating user %v (%v)", u.Username, retUser.Id)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("deactivating user %v on create: %w", u.Username, appErr))
				continue
			}
			retUser, appErr = s.API.GetUser(retUser.Id)
			if appErr != nil {
				l.WithError(appErr).Errorf("refetching user %v (%v) for validation on create", u.Username, retUser.Id)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("refetching user %v for validation on create: %w", u.Username, appErr))
				continue
			}
		}

		var err error
		out[n], err = mattermostUserToSyncUser(retUser)
		if err != nil {
			l.WithError(err).Errorf("validating created user %v (%v)", u.Username, retUser.Id)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("validating created user %v: %w", u.Username, err))
			continue
		}
	}
	if mergedErr != nil {
		return nil, mergedErr
	}
	return out, nil
}

func (s *MattermostService) CreateGroups(ctx context.Context, groups []*Group[int]) ([]*Group[string], error) {
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

		ng, err := mattermostGroupToSyncGroup(ctx, s, serviceGroup)
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

func (s *MattermostService) UpdateUsers(ctx context.Context, users []*User[string]) ([]*User[string], []bool, error) {
	l := ctxlog.FromContext(ctx)
	var mergedErr error
	out := make([]*User[string], len(users))
	updated := make([]bool, len(users))
	for n, u := range users {
		if u.ServiceUser == nil {
			serviceUser, appErr := s.API.GetUser(u.UserID)
			if appErr != nil {
				l.WithError(appErr).Errorf("fetching user %v (%v) for update", u.Username, u.UserID)
				return nil, nil, fmt.Errorf("fetching user %v for update: %w", u.UserID, appErr)
			}
			u.ServiceUser = serviceUser
		}

		serviceUser := u.ServiceUser.(*model.User)

		// Update basic properties.
		propsUpdated := false
		if serviceUser.Username != u.Username {
			serviceUser.Username = u.Username
			propsUpdated = true
		}
		if serviceUser.Email != u.Email {
			serviceUser.Email = u.Email
			propsUpdated = true
		}
		if serviceUser.Nickname != u.DisplayName {
			serviceUser.Nickname = u.DisplayName
			propsUpdated = true
		}
		if serviceUser.Props == nil {
			serviceUser.Props = map[string]string{}
		}
		if serviceUser.Props[MMIdPUsernameProp] != u.Username {
			serviceUser.Props[MMIdPUsernameProp] = u.Username
			propsUpdated = true
		}
		if propsUpdated {
			newServiceUser, appErr := s.API.UpdateUser(serviceUser)
			if appErr != nil {
				l.WithError(appErr).Errorf("updating user %v (%v)", serviceUser.Username, serviceUser.Id)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("updating user %v: %w", serviceUser.Id, appErr))
				continue
			}
			u.ServiceUser = newServiceUser
			serviceUser = newServiceUser
			updated[n] = true
		}

		// Update active/inactive state.
		if (serviceUser.DeleteAt == 0) != u.Active {
			if appErr := s.API.UpdateUserActive(serviceUser.Id, u.Active); appErr != nil {
				l.WithError(appErr).Errorf("setting user %v (%v) to active=%v", serviceUser.Username, serviceUser.Id, u.Active)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("setting user %v to active=%v: %w", serviceUser.Id, u.Active, appErr))
				continue
			}
			newServiceUser, appErr := s.API.GetUser(serviceUser.Id)
			if appErr != nil {
				mergedErr = errors.Join(mergedErr, fmt.Errorf("reading back user %v after changing active state: %w", serviceUser.Id, appErr))
				continue
			}
			u.ServiceUser = newServiceUser
			serviceUser = newServiceUser
			updated[n] = true
		}

		u, err := mattermostUserToSyncUser(serviceUser)
		if err != nil {
			l.WithError(err).Errorf("validating user %v (%v) after update", serviceUser.Username, serviceUser.Id)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("validating user %v after update: %w", serviceUser.Id, err))
			continue
		}

		out[n] = u
	}
	return out, updated, nil
}

func (s *MattermostService) UpdateGroups(ctx context.Context, groups []*Group[string]) ([]*Group[string], []bool, error) {
	// Unimplemented.
	return groups, make([]bool, len(groups)), nil
}

func (s *MattermostService) DeleteGroups(ctx context.Context, groups []*Group[string]) error {
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

func (s *MattermostService) AddGroupMembers(ctx context.Context, groupID string, memberIDs []string) error {
	_, appErr := s.API.UpsertGroupMembers(groupID, memberIDs)
	if appErr != nil {
		ctxlog.FromContext(ctx).WithError(appErr).Errorf("adding group members to %v", groupID)
		return appErr
	}
	return nil
}

func (s *MattermostService) RemoveGroupMembers(ctx context.Context, groupID string, memberIDs []string) error {
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
