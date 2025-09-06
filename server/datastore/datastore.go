package datastore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
	"github.com/lukegb/mattermost-plugin-uffd/server/stringset"
	"github.com/lukegb/mattermost-plugin-uffd/server/syncengine"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type MattermostPluginAPI interface {
	KVSet(key string, value []byte) *model.AppError
	KVGet(key string) ([]byte, *model.AppError)
	KVDelete(key string) *model.AppError
}

var _ MattermostPluginAPI = (plugin.API)(nil)

type MattermostDataStore struct {
	API MattermostPluginAPI
}

func kvGet[T any](api MattermostPluginAPI, key string) (T, bool, error) {
	var v T

	res, err := api.KVGet(key)
	if err != nil {
		return v, false, err
	}
	if res == nil {
		return v, false, nil
	}

	if err := json.Unmarshal(res, &v); err != nil {
		return v, false, err
	}
	return v, true, nil
}

func kvSet[T any](api MattermostPluginAPI, key string, val T) error {
	bs, err := json.Marshal(val)
	if err != nil {
		return err
	}
	if err := api.KVSet(key, bs); err != nil {
		return err
	}
	return nil
}

func persistList[T any](ctx context.Context, api MattermostPluginAPI, prefix string, is []T, nameFun func(T) string) error {
	l := ctxlog.FromContext(ctx)
	persistKey := fmt.Sprintf("%s_persisted", prefix)

	// Clean up any no-longer-needed persisted groups so we don't keep them around forever.
	// This is best-effort; we'll overwrite groups_persisted after this regardless of whether the delete actually works.
	if previousGroups, ok, err := kvGet[[]string](api, persistKey); err != nil {
		return fmt.Errorf("reading %s failed: %w", persistKey, err)
	} else if ok {
		itemsToDelete := stringset.FromSlice(previousGroups)
		for _, i := range is {
			itemsToDelete.Remove(nameFun(i))
		}
		for _, i := range itemsToDelete.Sorted() {
			l.WithField("item", i).Infof("removing now-unnecessary previously persisted %s", prefix)
			if err := api.KVDelete(fmt.Sprintf("%s:%s", prefix, i)); err != nil {
				l.WithField("item", i).WithError(err).Errorf("failed to remove now-unnecessary previously persisted %s", prefix)
			}
		}
	}

	// Save these groups.
	newGroupNames := make([]string, 0, len(is))
	for _, i := range is {
		name := nameFun(i)
		newGroupNames = append(newGroupNames, name)
		if err := kvSet(api, fmt.Sprintf("%s:%s", prefix, name), i); err != nil {
			l.WithField("item", i).WithError(err).Errorf("failed to write %s to Mattermost KV store", prefix)
			return fmt.Errorf("writing %s %s to Mattermost KV store: %w", prefix, name, err)
		}
	}

	// Note down which groups we persisted.
	if err := kvSet(api, persistKey, newGroupNames); err != nil {
		l.WithError(err).Errorf("failed to save %s to Mattermost KV store", persistKey)
		// Non-fatal, let's just proceed...
	}
	return nil
}

func unpersistList[T any](ctx context.Context, api MattermostPluginAPI, prefix string) ([]T, error) {
	storedNames, ok, err := kvGet[[]string](api, fmt.Sprintf("%s_persisted", prefix))
	if err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}
	var out []T
	for _, n := range storedNames {
		el, ok, err := unpersistItem[T](ctx, api, prefix, n)
		if err != nil {
			return nil, fmt.Errorf("loading %v:%v from Mattermost KV store failed: %w", prefix, n, err)
		} else if !ok {
			return nil, fmt.Errorf("%v:%v missing from Mattermost KV store", prefix, n)
		}
		out = append(out, el)
	}
	return out, nil
}

func persistItem[T any](_ context.Context, api MattermostPluginAPI, prefix string, name string, item T) error {
	return kvSet[T](api, fmt.Sprintf("%s:%s", prefix, name), item)
}

func unpersistItem[T any](_ context.Context, api MattermostPluginAPI, prefix string, name string) (T, bool, error) {
	return kvGet[T](api, fmt.Sprintf("%s:%s", prefix, name))
}

// SaveGroups saves all the groups to the KV store.
func (s *MattermostDataStore) SaveGroups(ctx context.Context, groups []*syncengine.Group[string]) error {
	return persistList(ctx, s.API, "groups", groups, func(g *syncengine.Group[string]) string {
		return g.Name
	})
}

// LoadGroups fetches all the groups from the KV store.
func (s *MattermostDataStore) LoadGroups(ctx context.Context) ([]*syncengine.Group[string], error) {
	return unpersistList[*syncengine.Group[string]](ctx, s.API, "groups")
}

// LoadGroup fetches a group from the KV store.
func (s *MattermostDataStore) LoadGroup(ctx context.Context, name string) (*syncengine.Group[string], bool, error) {
	return unpersistItem[*syncengine.Group[string]](ctx, s.API, "groups", name)
}

// SaveTeams saves all the teams to the KV store.
func (s *MattermostDataStore) SaveTeams(ctx context.Context, teams []syncengine.Team) error {
	return persistList(ctx, s.API, "teams", teams, func(t syncengine.Team) string {
		return t.Name
	})
}

// LoadTeams fetches all the teams from the KV store.
func (s *MattermostDataStore) LoadTeams(ctx context.Context) ([]syncengine.Team, error) {
	return unpersistList[syncengine.Team](ctx, s.API, "teams")
}

// LoadTeam fetches a the team from the KV store.
func (s *MattermostDataStore) LoadTeam(ctx context.Context, name string) (syncengine.Team, bool, error) {
	return unpersistItem[syncengine.Team](ctx, s.API, "teams", name)
}

type ACLElementType string

const (
	ACLElementTypeTeamMember ACLElementType = "team_member"
	ACLElementTypeTeamLead   ACLElementType = "team_lead"
	ACLElementTypeGroup      ACLElementType = "group"
	ACLElementTypeUser       ACLElementType = "user"
)

type ACLElement struct {
	Type  ACLElementType
	Value string
}

type ChannelInfo struct {
	ID string

	Name                string
	MembershipUnmanaged bool // disables membership management for this channel

	Members []ACLElement
	Admins  []ACLElement
}

// SaveChannel saves the channel to the KV store.
func (s *MattermostDataStore) SaveChannel(ctx context.Context, channel *ChannelInfo) error {
	return persistItem[*ChannelInfo](ctx, s.API, "channels", channel.ID, channel)
}

// LoadChannel loads the channel from the KV store.
func (s *MattermostDataStore) LoadChannel(ctx context.Context, id string) (*ChannelInfo, bool, error) {
	return unpersistItem[*ChannelInfo](ctx, s.API, "channels", id)
}
