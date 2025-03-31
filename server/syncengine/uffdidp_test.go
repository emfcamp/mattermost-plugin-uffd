package syncengine

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/lukegb/mattermost-plugin-uffd/server/uffd"
)

type FakeUffd struct {
	Users  []uffd.User
	Groups []uffd.Group
}

var _ UffdAPI = ((*FakeUffd)(nil))

func (u *FakeUffd) GetUsers(context.Context) ([]uffd.User, error) {
	return append(([]uffd.User)(nil), u.Users...), nil
}

func (u *FakeUffd) GetUserByID(ctx context.Context, id int) (*uffd.User, error) {
	for _, u := range u.Users {
		if u.ID == id {
			return &u, nil
		}
	}
	return nil, uffd.ErrNotFound
}

func (u *FakeUffd) GetGroups(context.Context) ([]uffd.Group, error) {
	return append(([]uffd.Group)(nil), u.Groups...), nil
}

func TestUffdFetchUsers_NoEnabledGroup(t *testing.T) {
	t.Parallel()

	p := &UffdIDP{
		API: &FakeUffd{
			Users: []uffd.User{{
				ID:          1000,
				LoginName:   "lukegb",
				DisplayName: "Luke GB",
				Email:       "lukegb@example.com",
				Groups:      []string{"baseline_group"},
			}, {
				ID:          1001,
				LoginName:   "nogroupjohnny",
				DisplayName: "No Group Johnny",
				Email:       "nogroup@example.com",
				Groups:      []string{},
			}},
		},
		EnabledGroup: "",
	}

	got, err := p.FetchUsers(context.Background())
	if err != nil {
		t.Fatalf("FetchUsers: %v", err)
	}
	want := []*User[int]{{
		UserID:      1000,
		Username:    "lukegb",
		DisplayName: "Luke GB",
		Email:       "lukegb@example.com",
		Active:      true,
	}, {
		UserID:      1001,
		Username:    "nogroupjohnny",
		DisplayName: "No Group Johnny",
		Email:       "nogroup@example.com",
		Active:      false,
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchUser (-got +want):\n%s", diff)
	}
}

func TestUffdFetchUsers_EnabledGroupSet(t *testing.T) {
	t.Parallel()

	p := &UffdIDP{
		API: &FakeUffd{
			Users: []uffd.User{{
				ID:          1000,
				LoginName:   "lukegb",
				DisplayName: "Luke GB",
				Email:       "lukegb@example.com",
				Groups:      []string{"mattermost_enabled"},
			}, {
				ID:          1001,
				LoginName:   "nogroupjohnny",
				DisplayName: "No Group Johnny",
				Email:       "nogroup@example.com",
				Groups:      []string{"irrelevant_group"},
			}},
		},
		EnabledGroup: "mattermost_enabled",
	}

	got, err := p.FetchUsers(context.Background())
	if err != nil {
		t.Fatalf("FetchUsers: %v", err)
	}
	want := []*User[int]{{
		UserID:      1000,
		Username:    "lukegb",
		DisplayName: "Luke GB",
		Email:       "lukegb@example.com",
		Active:      true,
	}, {
		UserID:      1001,
		Username:    "nogroupjohnny",
		DisplayName: "No Group Johnny",
		Email:       "nogroup@example.com",
		Active:      false,
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchUser (-got +want):\n%s", diff)
	}
}

func TestUffdFetchUserByID(t *testing.T) {
	t.Parallel()

	p := &UffdIDP{
		API: &FakeUffd{
			Users: []uffd.User{{
				ID:          1000,
				LoginName:   "lukegb",
				DisplayName: "Luke GB",
				Email:       "lukegb@example.com",
				Groups:      []string{"mattermost_enabled"},
			}},
		},
	}

	got, err := p.FetchUserByID(context.Background(), 1000)
	if err != nil {
		t.Fatalf("FetchUserByID: %v", err)
	}
	want := &User[int]{
		UserID:      1000,
		Username:    "lukegb",
		DisplayName: "Luke GB",
		Email:       "lukegb@example.com",
		Active:      true,
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchUser (-got +want):\n%s", diff)
	}
}

func TestUffdFetchGroups(t *testing.T) {
	t.Parallel()

	p := &UffdIDP{
		API: &FakeUffd{
			Users: []uffd.User{{
				ID:          1000,
				LoginName:   "lukegb",
				DisplayName: "Luke GB",
				Email:       "lukegb@example.com",
				Groups:      []string{"baseline_group"},
			}, {
				ID:          1001,
				LoginName:   "nogroupjohnny",
				DisplayName: "No Group Johnny",
				Email:       "nogroup@example.com",
				Groups:      []string{},
			}},
			Groups: []uffd.Group{{
				ID:      2000,
				Name:    "baseline_group",
				Members: []string{"lukegb"},
			}},
		},
		EnabledGroup: "",
	}

	got, err := p.FetchGroups(context.Background())
	if err != nil {
		t.Fatalf("FetchGroups: %v", err)
	}
	want := []*Group[int]{{
		GroupID:       2000,
		Name:          "baseline_group",
		MemberUserIDs: []int{1000},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchGroups (-got +want):\n%s", diff)
	}
}

func TestUffdFetchGroups_Cache(t *testing.T) {
	t.Parallel()

	p := &UffdIDP{
		API: &FakeUffd{
			Groups: []uffd.Group{{
				ID:      2000,
				Name:    "baseline_group",
				Members: []string{"lukegb"},
			}},
		},
		EnabledGroup: "",
		usernameToUser: map[string]*User[int]{"lukegb": {
			UserID: 1000,
			Active: true,
		}},
	}

	got, err := p.FetchGroups(context.Background())
	if err != nil {
		t.Fatalf("FetchGroups: %v", err)
	}
	want := []*Group[int]{{
		GroupID:       2000,
		Name:          "baseline_group",
		MemberUserIDs: []int{1000},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchGroups (-got +want):\n%s", diff)
	}
}

func TestUffdFetchGroups_FilterRegex(t *testing.T) {
	t.Parallel()

	p := &UffdIDP{
		API: &FakeUffd{
			Users: []uffd.User{{
				ID:          1000,
				LoginName:   "lukegb",
				DisplayName: "Luke GB",
				Email:       "lukegb@example.com",
				Groups:      []string{"baseline_group", "mattermost_group"},
			}, {
				ID:          1001,
				LoginName:   "nogroupjohnny",
				DisplayName: "No Group Johnny",
				Email:       "nogroup@example.com",
				Groups:      []string{},
			}, {
				ID:          1002,
				LoginName:   "irrelevantgroupjames",
				DisplayName: "Irrelevant Group James",
				Email:       "irrelevantgroup@example.com",
				Groups:      []string{"baseline_group"},
			}},
			Groups: []uffd.Group{{
				ID:      2000,
				Name:    "baseline_group",
				Members: []string{"lukegb"},
			}, {
				ID:      2001,
				Name:    "mattermost_group",
				Members: []string{"lukegb"},
			}},
		},
		GroupFilterRegex: "^mattermost_.*$",
	}

	got, err := p.FetchGroups(context.Background())
	if err != nil {
		t.Fatalf("FetchGroups: %v", err)
	}
	want := []*Group[int]{{
		GroupID:       2001,
		Name:          "mattermost_group",
		MemberUserIDs: []int{1000},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchGroups (-got +want):\n%s", diff)
	}
}
