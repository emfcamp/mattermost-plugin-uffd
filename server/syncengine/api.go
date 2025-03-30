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
	IdPUserID     int
	ServiceUserID string
	ServiceUser   any
}

func (u User[I]) ShallowClone() *User[I] {
	return &u
}

type Group[I comparable] struct {
	GroupID       any
	Name          string
	IdPID         string
	MemberUserIDs []I

	ServiceGroup any
}

func (g Group[I]) ShallowClone() *Group[I] {
	return &g
}

type IdPAPI interface {
	FetchUsers(context.Context) ([]*User[int], error)
	FetchGroups(context.Context) ([]*Group[int], error)

	FetchUserByID(ctx context.Context, uid int) (*User[int], error)
}

type ServiceAPI interface {
	FetchUsers(context.Context) ([]*User[string], error)
	FetchGroups(context.Context) ([]*Group[string], error)

	CreateUsers(context.Context, []*User[int]) ([]*User[string], error)
	CreateGroups(context.Context, []*Group[int]) ([]*Group[string], error)

	UpdateUsers(context.Context, []*User[string]) ([]*User[string], []bool, error)
	UpdateGroups(context.Context, []*Group[string]) ([]*Group[string], []bool, error)

	AddGroupMembers(ctx context.Context, groupID string, members []string) error
	RemoveGroupMembers(ctx context.Context, groupID string, members []string) error
	DeleteGroups(context.Context, []*Group[string]) error
}
