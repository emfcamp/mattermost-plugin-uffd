package syncablesync

import (
	"context"
	"fmt"
	"strings"

	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"
	"github.com/mattermost/mattermost/server/public/model"
)

type mattermostSystemRoleHandler struct {
	api mattermostSystemRoleAPI
}

type mattermostSystemRoleAPI interface {
	GetUser(userID string) (*model.User, *model.AppError)
	GetUsers(*model.UserGetOptions) ([]*model.User, *model.AppError)
	UpdateUserRoles(string, string) (*model.User, *model.AppError)
}

func (h *mattermostSystemRoleHandler) FetchRoster(ctx context.Context, st SyncableTarget) ([]RosterMember, error) {
	if st.Type != mattermostSyncableTypeSystemRole {
		return nil, fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeSystemRole)
	}

	roleHolders, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]*model.User, error) {
		us, appErr := h.api.GetUsers(&model.UserGetOptions{
			Roles:   []string{st.ID},
			Page:    page,
			PerPage: perPage,
		})
		if appErr != nil {
			return nil, appErr
		}
		return us, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching channel members for channel %v: %w", st.ID, err)
	}

	out := make([]RosterMember, 0, len(roleHolders))
	for _, u := range roleHolders {
		if _, ok := u.Props[syncengine.MMIdPUserIDProp]; u.AuthService != "openid" || !ok {
			// We ignore non-IdP users for this, to allow for a local admin account in case things go really sideways.
			continue
		}
		out = append(out, RosterMember{
			UserID: u.Id,

			ServiceType: u,
		})
	}
	return out, nil
}

func (h *mattermostSystemRoleHandler) frobRoles(ctx context.Context, st SyncableTarget, rms []RosterMember, f func(stringset.Set[string]) stringset.Set[string]) error {
	if st.Type != mattermostSyncableTypeSystemRole {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeSystemRole)
	}

	for _, rm := range rms {
		var u *model.User
		if rm.ServiceType != nil {
			u = rm.ServiceType.(*model.User)
		} else {
			// Fetch the user by ID.
			var appErr *model.AppError
			u, appErr = h.api.GetUser(rm.UserID)
			if appErr != nil {
				return fmt.Errorf("fetching user %v: %w", rm.UserID, appErr)
			}
		}

		if _, ok := u.Props[syncengine.MMIdPUserIDProp]; u.AuthService != "openid" || !ok {
			// We ignore non-IdP users for this, to allow for a local admin account in case things go really sideways.
			return fmt.Errorf("refusing to manage non-IdP user %v (%v)", u.Username, u.Id)
		}

		roles := f(stringset.FromSlice(u.GetRoles()))
		newRolesStr := strings.Join(roles.Sorted(), " ")
		u, appErr := h.api.UpdateUserRoles(u.Id, newRolesStr)
		if appErr != nil {
			return fmt.Errorf("setting %s roles to %s: %w", rm.UserID, roles, appErr)
		}
		rm.ServiceType = u
	}
	return nil
}

func (h *mattermostSystemRoleHandler) AddMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	return h.frobRoles(ctx, st, rms, func(roles stringset.Set[string]) stringset.Set[string] {
		roles.Add(st.ID)
		return roles
	})
}

func (h *mattermostSystemRoleHandler) DeleteMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	return h.frobRoles(ctx, st, rms, func(roles stringset.Set[string]) stringset.Set[string] {
		roles.Remove(st.ID)
		return roles
	})
}

func (h *mattermostSystemRoleHandler) UpdateMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	return fmt.Errorf("updating members doesn't make sense for system roles")
}
