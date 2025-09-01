package syncengine

import (
	"context"
)

type User[I comparable] struct {
	UserID      I
	Username    string
	DisplayName string
	Email       string
	Active      bool

	// These fields are only populated on responses from the service, not the IdP.
	IDPUserID     int
	ServiceUserID string
	ServiceUser   any
}

func (u User[I]) ShallowClone() *User[I] {
	return &u
}

type Group[I comparable] struct {
	GroupID       any
	Name          string
	IDPID         string
	MemberUserIDs []I

	ServiceGroup any
}

func (g Group[I]) ShallowClone() *Group[I] {
	return &g
}

type IDPAPI interface {
	FetchUsers(context.Context) ([]*User[int], error)
	FetchGroups(context.Context) ([]*Group[int], error)

	FetchUserByID(ctx context.Context, uid int) (*User[int], error)
}

// Team represents a group of people, of which some are team leads.
type Team struct {
	Name    string
	Leads   []string
	Members []string
}

type ServiceAPI interface {
	FetchUsers(context.Context) ([]*User[string], error)
	CreateUsers(context.Context, []*User[int]) ([]*User[string], error)
	UpdateUsers(context.Context, []*User[string]) ([]*User[string], []bool, error)
}

type DataStoreAPI interface {
	SaveTeams(context.Context, []Team) error
	SaveGroups(context.Context, []*Group[string]) error
}
