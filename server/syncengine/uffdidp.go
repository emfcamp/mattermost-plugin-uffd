package syncengine

import (
	"context"
	"fmt"
	"regexp"

	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/lukegb/mattermost-plugin-uffd/server/uffd"
)

type UffdAPI interface {
	GetUsers(context.Context) ([]uffd.User, error)
	GetGroups(context.Context) ([]uffd.Group, error)
	GetUserByID(context.Context, int) (*uffd.User, error)
}

var _ UffdAPI = (*uffd.API)(nil)

type UffdIdP struct {
	API UffdAPI

	// EnabledGroup, if non-empty, is a group name which dictates whether the service should see the account as enabled.
	// If empty, then presence in _any_ groups will mean the account is enabled.
	EnabledGroup string

	// GroupFilterRegex, if non-empty, restricts the set of groups which will be synced into Mattermost.
	GroupFilterRegex string

	usernameToUser map[string]*User[int]
}

var _ IdPAPI = (*UffdIdP)(nil)

func (idp *UffdIdP) uffdUserToSyncUser(u uffd.User) (*User[int], error) {
	active := len(u.Groups) > 0
	if idp.EnabledGroup != "" {
		active = stringset.FromSlice(u.Groups).Contains(idp.EnabledGroup)
	}

	return &User[int]{
		UserID:      u.ID,
		Username:    u.LoginName,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Active:      active,
	}, nil
}

func (idp *UffdIdP) FetchUsers(ctx context.Context) ([]*User[int], error) {
	idpUsers, err := idp.API.GetUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching users from uffd: %w", err)
	}
	idp.populateUserCache(idpUsers)

	out := make([]*User[int], len(idpUsers))
	for n, idpUser := range idpUsers {
		out[n], err = idp.uffdUserToSyncUser(idpUser)
		if err != nil {
			return nil, fmt.Errorf("transforming user %v: %w", idpUser.LoginName, err)
		}
	}
	return out, nil
}

func (idp *UffdIdP) FetchUserByID(ctx context.Context, userID int) (*User[int], error) {
	idpUser, err := idp.API.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("fetching user from uffd: %w", err)
	}

	return idp.uffdUserToSyncUser(*idpUser)
}

func (idp *UffdIdP) populateUserCache(idpUsers []uffd.User) (map[string]*User[int], error) {
	un2U := make(map[string]*User[int], len(idpUsers))
	for _, u := range idpUsers {
		var err error
		un2U[u.LoginName], err = idp.uffdUserToSyncUser(u)
		if err != nil {
			return nil, err
		}
	}
	idp.usernameToUser = un2U
	return un2U, nil
}

func (idp *UffdIdP) loadUserCache(ctx context.Context) (map[string]*User[int], error) {
	if idp.usernameToUser != nil {
		return idp.usernameToUser, nil
	}

	idpUsers, err := idp.API.GetUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching users from uffd: %w", err)
	}
	return idp.populateUserCache(idpUsers)
}

func (idp *UffdIdP) uffdGroupToSyncGroup(ctx context.Context, g uffd.Group) (*Group[int], error) {
	usernameToUser, err := idp.loadUserCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching username<->UID cache: %w", err)
	}

	memberIDs := make([]int, 0, len(g.Members))
	for _, username := range g.Members {
		user, ok := usernameToUser[username]
		if !ok {
			continue
		}
		if !user.Active {
			continue
		}

		memberIDs = append(memberIDs, usernameToUser[username].UserID)
	}

	return &Group[int]{
		GroupID:       g.ID,
		Name:          g.Name,
		MemberUserIDs: memberIDs,
	}, nil
}

func (idp *UffdIdP) FetchGroups(ctx context.Context) ([]*Group[int], error) {
	idpGroups, err := idp.API.GetGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching groups from uffd: %w", err)
	}

	var restrictRe *regexp.Regexp
	if idp.GroupFilterRegex != "" {
		var err error
		restrictRe, err = regexp.Compile(idp.GroupFilterRegex)
		if err != nil {
			return nil, fmt.Errorf("compiling group filter regex %q: %w", idp.GroupFilterRegex, err)
		}
	}

	out := make([]*Group[int], 0, len(idpGroups))
	for _, idpGroup := range idpGroups {
		if restrictRe != nil && !restrictRe.MatchString(idpGroup.Name) {
			continue
		}

		syncGroup, err := idp.uffdGroupToSyncGroup(ctx, idpGroup)
		if err != nil {
			return nil, fmt.Errorf("transforming group %v: %w", idpGroup.Name, err)
		}
		out = append(out, syncGroup)
	}
	return out, nil
}
