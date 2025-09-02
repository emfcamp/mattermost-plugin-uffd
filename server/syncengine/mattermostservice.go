package syncengine

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
)

const (
	MMIdPUserIDProp   = "idp/userid"
	MMIdPUsernameProp = "idp/username"

	mmDefaultPageSize = 1000
)

type mattermostPluginAPI interface {
	GetUsers(*model.UserGetOptions) ([]*model.User, *model.AppError)
	CreateUser(*model.User) (*model.User, *model.AppError)
	GetUser(userID string) (*model.User, *model.AppError)
	GetUserByEmail(userID string) (*model.User, *model.AppError)
	GetUserByUsername(name string) (*model.User, *model.AppError)
	UpdateUser(*model.User) (*model.User, *model.AppError)
	UpdateUserActive(userID string, active bool) *model.AppError
	UpdateUserAuth(userID string, userAuth *model.UserAuth) (*model.UserAuth, *model.AppError)

	CreateSession(session *model.Session) (*model.Session, *model.AppError)
}

var _ mattermostPluginAPI = (plugin.API)(nil)

type MattermostService struct {
	API mattermostPluginAPI
}

var _ ServiceAPI = (*MattermostService)(nil)

type strError string

func (s strError) Error() string { return string(s) }

var ErrNotIDPUser strError = "not a user created from the IdP"

func mattermostUserToSyncUser(serviceUser *model.User) (*User[string], error) {
	idpUserIDStr := serviceUser.Props[MMIdPUserIDProp]
	if idpUserIDStr == "" {
		return nil, fmt.Errorf("loading user %v: %w", serviceUser.Id, ErrNotIDPUser)
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

		IDPUserID:     idpUserID,
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
		if errors.Is(err, ErrNotIDPUser) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("fetching users from Mattermost: %w", err)
		}
		out = append(out, u)
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
			// Try to fetch them by email instead; if we can, then we'll actually update them.
			var appErrByEmail *model.AppError
			retUser, appErrByEmail = s.API.GetUserByEmail(u.Email)
			if appErrByEmail != nil {
				l.WithError(appErrByEmail).Errorf("trying to fetch user %v by email %v", u.Username, u.Email)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("creating user %v: %w", u.Username, appErr))
				continue
			}

			// We have a user, sync their state.
			if _, appErr := s.API.UpdateUserAuth(retUser.Id, &model.UserAuth{
				AuthService: "openid",
				AuthData:    &authData,
			}); appErr != nil {
				l.WithError(appErr).Errorf("second-chance (by email) updating user auth to OAuth for %v (%v, email: %v)", retUser.Username, retUser.Id, retUser.Email)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("updating found-by-email user %v's auth service to OAuth: %w", retUser.Id, appErr))
				continue
			}
			retUser.Username = u.Username
			retUser.Email = u.Email
			retUser.Nickname = u.DisplayName
			if retUser.Props == nil {
				retUser.Props = map[string]string{}
			}
			retUser.Props[MMIdPUsernameProp] = u.Username
			retUser.Props[MMIdPUserIDProp] = authData
			retUser, appErr = s.API.UpdateUser(retUser)
			if appErr != nil {
				l.WithError(appErr).Errorf("second-chance (by email) updating user %v (%v, email: %v)", retUser.Username, retUser.Id, retUser.Email)
				mergedErr = errors.Join(mergedErr, fmt.Errorf("updating found-by-email user %v: %w", retUser.Id, appErr))
				continue
			}
			// We found an account with the same email and joined it to OIDC.
			// Now we handle it as though we just freshly creatd it.
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
		if serviceUser.FirstName != u.DisplayName {
			serviceUser.FirstName = u.DisplayName
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
