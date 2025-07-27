package syncengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/paginator"
	"github.com/mattermost/mattermost/server/public/model"
)

// MattermostRESTGroupBackend uses the Mattermost REST API to manipulate groups as a bot account.
//
// This doesn't require an Enterprise license.
type MattermostRESTGroupBackend struct {
	PluginAPI mattermostPluginAPI

	// RESTAPI can be set to use a particular client. Credentials need not be supplied; they will be automatically generated.
	RESTAPI mattermostRESTAPI

	mu               sync.Mutex
	sessionExpiresAt time.Time
}

var _ MattermostGroupBackend = ((*MattermostRESTGroupBackend)(nil))

type mattermostRESTAPI interface {
	CreateGroup(ctx context.Context, group *model.Group) (*model.Group, *model.Response, error)
	GetGroups(ctx context.Context, opts model.GroupSearchOpts) ([]*model.Group, *model.Response, error)
	DeleteGroup(ctx context.Context, groupID string) (*model.Group, *model.Response, error)
	RestoreGroup(ctx context.Context, groupID string, etag string) (*model.Group, *model.Response, error)
	PatchGroup(ctx context.Context, groupID string, patch *model.GroupPatch) (*model.Group, *model.Response, error)

	// GetGroupMembers(ctx context.Context, groupID string) (*model.GroupMemberList, *model.Response, error) // This API is useless because it doesn't paginate. WTF?
	DoAPIGet(ctx context.Context, url string, etag string) (*http.Response, error)
	UpsertGroupMembers(ctx context.Context, groupID string, userIds *model.GroupModifyMembers) ([]*model.GroupMember, *model.Response, error)
	DeleteGroupMembers(ctx context.Context, groupID string, userIds *model.GroupModifyMembers) ([]*model.GroupMember, *model.Response, error)
}

var _ mattermostRESTAPI = ((*model.Client4)(nil))

func mattermostRESTGroupToSyncGroup(ctx context.Context, s *MattermostRESTGroupBackend, serviceGroup *model.Group) (*Group[string], error) {
	match := extractGroupIDRegex.FindAllStringSubmatch(serviceGroup.Description, -1)
	if len(match) < 1 {
		return nil, fmt.Errorf("group description %q doesn't match expectations", serviceGroup.Description)
	}

	groupMembers, err := paginator.FetchPaginated(mmDefaultPageSize, func(page, perPage int) ([]string, error) {
		list, _, err := s.getGroupMembersButPaginated(ctx, serviceGroup.Id, page, perPage)
		if err != nil {
			return nil, err
		}
		userIDs := make([]string, len(list.Members))
		for n, u := range list.Members {
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
		IDPID:         match[0][1],
		MemberUserIDs: groupMembers,

		ServiceGroup: serviceGroup,
	}, nil
}

func (m *MattermostRESTGroupBackend) getSystemBot(ctx context.Context) (*model.User, error) {
	l := ctxlog.FromContext(ctx)
	u, appErr := m.PluginAPI.GetUserByUsername(model.BotSystemBotUsername)
	if appErr != nil {
		l.WithError(appErr).Errorf("getting system bot user (looking for username %q)", model.BotSystemBotUsername)
		return nil, appErr
	}
	return u, nil
}

func (m *MattermostRESTGroupBackend) ensureCredentials(ctx context.Context) error {
	client, ok := m.RESTAPI.(*model.Client4)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	// Is the session still valid?
	if client.AuthToken != "" {
		if now.After(m.sessionExpiresAt) {
			client.AuthToken = ""
			m.sessionExpiresAt = time.Time{}
		} else {
			return nil
		}
	}

	u, err := m.getSystemBot(ctx)
	if err != nil {
		return fmt.Errorf("getSystemBot: %w", err)
	}

	sess := &model.Session{
		UserId:    u.Id,
		Roles:     u.GetRawRoles(),
		DeviceId:  "",
		IsOAuth:   false,
		CreateAt:  now.UnixMilli(),
		ExpiresAt: now.Add(6 * time.Hour).UnixMilli(),
	}
	sess.GenerateCSRF()
	sess, appErr := m.PluginAPI.CreateSession(sess)
	if appErr != nil {
		return fmt.Errorf("CreateSession for system bot: %w", appErr)
	}
	m.sessionExpiresAt = time.UnixMilli(sess.ExpiresAt).Add(-30 * time.Minute) // add a safety margin to refresh the session early
	client.SetToken(sess.Token)
	return nil
}

func (m *MattermostRESTGroupBackend) getGroupMembersButPaginated(ctx context.Context, groupID string, page, perPage int) (*model.GroupMemberList, *model.Response, error) {
	query := fmt.Sprintf("?page=%v&per_page=%v", page, perPage)
	groupRoute := fmt.Sprintf("/groups/%s/members", groupID)
	r, err := m.RESTAPI.DoAPIGet(ctx, groupRoute+query, "")
	if err != nil {
		return nil, model.BuildResponse(r), err
	}
	defer func() {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
	}()
	var ml model.GroupMemberList
	if err := json.NewDecoder(r.Body).Decode(&ml); err != nil {
		return nil, nil, model.NewAppError("getGroupsMembersButPaginated", "api.unmarshal_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
	return &ml, model.BuildResponse(r), nil
}

var extractGroupIDRegex = regexp.MustCompile(`^uffd group \(([^)]+)\) - .*`)

// CreateGroups implements MattermostGroupBackend.
func (m *MattermostRESTGroupBackend) CreateGroups(ctx context.Context, groups []*Group[int]) ([]*Group[string], error) {
	l := ctxlog.FromContext(ctx)
	if err := m.ensureCredentials(ctx); err != nil {
		return nil, fmt.Errorf("ensureCredentials: %w", err)
	}
	out := make([]*Group[string], len(groups))
	var mergedErr error
outLoop:
	for n, g := range groups {
		groupDesc := fmt.Sprintf("uffd group (%d) - %v", g.GroupID, g.Name)

		groups, _, err := m.RESTAPI.GetGroups(ctx, model.GroupSearchOpts{
			Q:               g.Name,
			IncludeArchived: true,
		})
		if err != nil {
			l.WithError(err).Errorf("checking for archived group %v", g.Name)
		} else if len(groups) > 0 {
			for _, exGroup := range groups {
				if exGroup.GetName() != g.Name {
					continue
				}
				if exGroup.DeleteAt != 0 {
					if _, _, err := m.RESTAPI.RestoreGroup(ctx, exGroup.Id, ""); err != nil {
						l.WithError(err).Errorf("restoring previously-existing archived group %v / %v", exGroup.Id, g.Name)
						mergedErr = errors.Join(mergedErr, fmt.Errorf("restoring archived group %v / %v: %w", exGroup.Id, g.Name, err))
						continue outLoop
					}
				}
				allowRef := true
				if _, _, err := m.RESTAPI.PatchGroup(ctx, exGroup.Id, &model.GroupPatch{
					DisplayName:    &g.Name, // DisplayName is the "human" name, Name is the "mention" name
					Description:    &groupDesc,
					AllowReference: &allowRef,
				}); err != nil {
					l.WithError(err).Errorf("updating previously-existing archived group %v / %v", exGroup.Id, g.Name)
					mergedErr = errors.Join(mergedErr, fmt.Errorf("updating archived group %v / %v: %w", exGroup.Id, g.Name, err))
					continue outLoop
				}
			}
		}

		serviceGroup, _, err := m.RESTAPI.CreateGroup(ctx, &model.Group{
			Name:           &g.Name,
			DisplayName:    g.Name,
			Description:    groupDesc,
			Source:         model.GroupSourceCustom, // this is the only thing we can use without an LDAP Groups license
			AllowReference: true,
		})
		if err != nil {
			l.WithError(err).Errorf("creating group %v", g.Name)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("creating group %v: %w", g.Name, err))
			continue
		}

		ng, err := mattermostRESTGroupToSyncGroup(ctx, m, serviceGroup)
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

// FetchGroups implements MattermostGroupBackend.
func (m *MattermostRESTGroupBackend) FetchGroups(ctx context.Context) ([]*Group[string], error) {
	if err := m.ensureCredentials(ctx); err != nil {
		return nil, fmt.Errorf("ensureCredentials: %w", err)
	}
	groups, err := paginator.FetchPaginated(mmDefaultPageSize, func(page, perPage int) ([]*Group[string], error) {
		serviceGroups, _, err := m.RESTAPI.GetGroups(ctx, model.GroupSearchOpts{
			Source: model.GroupSourceCustom,
			PageOpts: &model.PageOpts{
				Page:    page,
				PerPage: perPage,
			},
		})
		var out []*Group[string]
		for _, serviceGroup := range serviceGroups {
			match := extractGroupIDRegex.FindAllStringSubmatch(serviceGroup.Description, -1)
			if len(match) < 1 {
				continue
			}

			outGroup, err := mattermostRESTGroupToSyncGroup(ctx, m, serviceGroup)
			if err != nil {
				return nil, fmt.Errorf("loading details for Mattermost group %v (%v): %w", serviceGroup.Id, serviceGroup.Name, err)
			}
			out = append(out, outGroup)
		}
		return out, err
	})
	if err != nil {
		return nil, fmt.Errorf("fetching groups from Mattermost: %w", err)
	}
	return groups, nil
}

// DeleteGroups implements MattermostGroupBackend.
func (m *MattermostRESTGroupBackend) DeleteGroups(ctx context.Context, groups []*Group[string]) error {
	if err := m.ensureCredentials(ctx); err != nil {
		return fmt.Errorf("ensureCredentials: %w", err)
	}
	l := ctxlog.FromContext(ctx)
	var mergedErr error
	for _, group := range groups {
		if _, _, err := m.RESTAPI.DeleteGroup(ctx, group.GroupID.(string)); err != nil {
			l.WithError(err).Errorf("deleting group %v (%v)", group.Name, group.GroupID)
			mergedErr = errors.Join(mergedErr, fmt.Errorf("deleting group %v: %w", group.Name, err))
		}
	}
	return mergedErr
}

// AddGroupMembers implements MattermostGroupBackend.
func (m *MattermostRESTGroupBackend) AddGroupMembers(ctx context.Context, groupID string, members []string) error {
	if err := m.ensureCredentials(ctx); err != nil {
		return fmt.Errorf("ensureCredentials: %w", err)
	}
	_, _, err := m.RESTAPI.UpsertGroupMembers(ctx, groupID, &model.GroupModifyMembers{
		UserIds: members,
	})
	if err != nil {
		ctxlog.FromContext(ctx).WithError(err).Errorf("adding group members to %v", groupID)
		return err
	}
	return nil
}

// RemoveGroupMembers implements MattermostGroupBackend.
func (m *MattermostRESTGroupBackend) RemoveGroupMembers(ctx context.Context, groupID string, members []string) error {
	if err := m.ensureCredentials(ctx); err != nil {
		return fmt.Errorf("ensureCredentials: %w", err)
	}
	_, _, err := m.RESTAPI.DeleteGroupMembers(ctx, groupID, &model.GroupModifyMembers{
		UserIds: members,
	})
	return err
}
