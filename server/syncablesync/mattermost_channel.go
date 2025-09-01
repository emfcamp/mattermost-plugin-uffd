package syncablesync

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/lukegb/mattermost-plugin-uffd/server/datastore"
	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
)

type mattermostChannelHandler struct {
	api  mattermostChannelAPI
	rest mattermostChannelREST
}

type mattermostChannelAPI interface {
	datastore.MattermostPluginAPI

	GetChannel(channelId string) (*model.Channel, *model.AppError)
	GetChannelMembers(channelID string, page, perPage int) (model.ChannelMembers, *model.AppError)
	AddUserToChannel(channelID, userID, onBehalfOfUser string) (*model.ChannelMember, *model.AppError)
	DeleteChannelMember(channelID, userID string) *model.AppError
	UpdateChannelMemberRoles(channelID, userID, roles string) (*model.ChannelMember, *model.AppError)
}

type mattermostChannelREST interface {
	GetScheme(ctx context.Context, id string) (*model.Scheme, *model.Response, error)
}

func (h *mattermostChannelHandler) FetchRoster(ctx context.Context, st SyncableTarget) ([]RosterMember, error) {
	if st.Type != mattermostSyncableTypeChannel {
		return nil, fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}

	channelMembers, err := paginator.FetchPaginated(perPageDefault, func(page, perPage int) ([]model.ChannelMember, error) {
		ms, appErr := h.api.GetChannelMembers(st.ID, page, perPage)
		if appErr != nil {
			return nil, appErr
		}
		return ms, nil
	})
	if err != nil {
		return nil, fmt.Errorf("fetching channel members for channel %v: %w", st.ID, err)
	}

	out := make([]RosterMember, len(channelMembers))
	for n, cm := range channelMembers {
		out[n] = RosterMember{
			UserID:  cm.UserId,
			IsAdmin: cm.SchemeAdmin,

			ServiceType: &cm,
		}
	}
	return out, nil
}

func (h *mattermostChannelHandler) getChannelScheme(ctx context.Context, st SyncableTarget) (*model.Scheme, error) {
	ch, appErr := h.api.GetChannel(st.ID)
	if appErr != nil {
		return nil, fmt.Errorf("getting channel %v for its scheme: %w", st.ID, appErr)
	}
	if ch.SchemeId == nil {
		return &model.Scheme{
			DefaultChannelAdminRole: "channel_admin",
			DefaultChannelUserRole:  "channel_user",
		}, nil
	}
	sch, _, err := h.rest.GetScheme(ctx, *ch.SchemeId)
	if err != nil {
		return nil, fmt.Errorf("getting channel %v's scheme %v: %w", st.ID, ch.SchemeId, err)
	}
	return sch, nil
}

func (h *mattermostChannelHandler) AddMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	// TODO: if a member is supposed to be a member of a channel, but isn't a member of the enclosing team, what do we do?
	// Maybe we should add them to the team automatically?
	if st.Type != mattermostSyncableTypeChannel {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}
	for _, rm := range rms {
		_, appErr := h.api.AddUserToChannel(st.ID, rm.UserID, "")
		if appErr != nil {
			return fmt.Errorf("adding %s to channel %s: %w", rm.UserID, st.ID, appErr)
		}
		if rm.IsAdmin {
			sch, err := h.getChannelScheme(ctx, st)
			if err != nil {
				return fmt.Errorf("getting channel %s permission scheme (so that I could updating channel member %s in channel %s to admin): %w", st.ID, rm.UserID, st.ID, err)
			}
			_, appErr := h.api.UpdateChannelMemberRoles(st.ID, rm.UserID, fmt.Sprintf("%s %s", sch.DefaultChannelUserRole, sch.DefaultChannelAdminRole))
			if appErr != nil {
				return fmt.Errorf("updating channel member %s in channel %s to admin: %w", rm.UserID, st.ID, appErr)
			}
		}
	}
	return nil
}

func (h *mattermostChannelHandler) DeleteMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeChannel {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}
	for _, rm := range rms {
		appErr := h.api.DeleteChannelMember(st.ID, rm.UserID)
		if appErr != nil {
			return fmt.Errorf("removing %s from channel %s: %w", rm.UserID, st.ID, appErr)
		}
	}
	return nil
}

func (h *mattermostChannelHandler) UpdateMembers(ctx context.Context, st SyncableTarget, rms []RosterMember) error {
	if st.Type != mattermostSyncableTypeChannel {
		return fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}
	sch, err := h.getChannelScheme(ctx, st)
	if err != nil {
		return fmt.Errorf("getting channel %s permission scheme: %w", st.ID, err)
	}
	for _, rm := range rms {

		oldTM := rm.ServiceType.(*model.ChannelMember)
		roles := stringset.FromSlice(oldTM.GetRoles())
		isAdmin := roles.Contains(sch.DefaultChannelAdminRole)

		if rm.IsAdmin != isAdmin {
			if rm.IsAdmin {
				roles.Add(sch.DefaultChannelAdminRole)
			} else {
				roles.Remove(sch.DefaultChannelAdminRole)
			}

			_, appErr := h.api.UpdateChannelMemberRoles(st.ID, rm.UserID, strings.Join(roles.Sorted(), " "))
			if appErr != nil {
				return fmt.Errorf("updating roles for %s in channel %s to %s: %w", rm.UserID, st.ID, roles.Sorted(), appErr)
			}
		}
	}
	return nil
}

func (h *mattermostChannelHandler) IsAddOnlyTarget(ctx context.Context, st SyncableTarget) (bool, error) {
	if st.Type != mattermostSyncableTypeChannel {
		return false, fmt.Errorf("passed wrong SyncableTarget %#v, can only handle %v", st, mattermostSyncableTypeChannel)
	}

	ch, appErr := h.api.GetChannel(st.ID)
	if appErr != nil {
		return false, fmt.Errorf("fetching channel %s: %w", st.ID, appErr)
	}
	if ch.IsOpen() {
		return true, nil
	}

	ds := &datastore.MattermostDataStore{API: h.api}
	chInfo, ok, err := ds.LoadChannel(ctx, st.ID)
	if err != nil {
		return false, fmt.Errorf("fetching auxiliary channel info %s: %w", st.ID, err)
	}
	if ok && chInfo.MembershipUnmanaged {
		return true, nil
	}
	return false, nil
}
