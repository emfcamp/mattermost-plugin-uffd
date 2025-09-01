package syncengine

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeMattermostPluginAPI struct {
	Users   []*model.User
	KVStore map[string][]byte
}

// KVDelete implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) KVDelete(key string) *model.AppError {
	if f.KVStore != nil {
		delete(f.KVStore, key)
	}
	return nil
}

// KVGet implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) KVGet(key string) ([]byte, *model.AppError) {
	val, ok := f.KVStore[key]
	if !ok {
		return nil, model.NewAppError("test", "test", nil, "no such key", 500)
	}
	return val, nil
}

// KVSet implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) KVSet(key string, value []byte) *model.AppError {
	if f.KVStore == nil {
		f.KVStore = make(map[string][]byte)
	}
	f.KVStore[key] = value
	return nil
}

// CreateUser implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) CreateUser(u *model.User) (*model.User, *model.AppError) {
	// Emails must be unique
	for _, u2 := range f.Users {
		if u.Email == u2.Email {
			return nil, model.NewAppError("test", "test", nil, "user with email already exists", 500)
		}
	}

	u = u.DeepCopy()
	u.Id = fmt.Sprintf("id::user::%v", u.Username)
	f.Users = append(f.Users, u.DeepCopy())
	return u.DeepCopy(), nil
}

func slicePage[T any](slice []T, page int, perPage int) []T {
	start := perPage * page
	end := perPage * (page + 1)
	if start > len(slice) {
		return nil
	}
	if end > len(slice) {
		end = len(slice)
	}
	return slice[start:end]
}

func xmap[T any](xs []T, f func(t T) T) []T {
	out := make([]T, len(xs))
	for n, x := range xs {
		out[n] = f(x)
	}
	return out
}

// GetUser implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetUser(userID string) (*model.User, *model.AppError) {
	for _, u := range f.Users {
		if u.Id == userID {
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// GetUserByEmail implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetUserByEmail(email string) (*model.User, *model.AppError) {
	for _, u := range f.Users {
		if u.Email == email {
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// GetUserByUsername implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetUserByUsername(username string) (*model.User, *model.AppError) {
	for _, u := range f.Users {
		if u.Username == username {
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// GetUsers implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) GetUsers(opts *model.UserGetOptions) ([]*model.User, *model.AppError) {
	return xmap(slicePage(f.Users, opts.Page, opts.PerPage), (*model.User).DeepCopy), nil
}

// UpdateUser implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) UpdateUser(in *model.User) (*model.User, *model.AppError) {
	for _, u := range f.Users {
		if u.Id == in.Id {
			u.Username = in.Username
			u.Email = in.Email
			u.EmailVerified = in.EmailVerified
			u.Nickname = in.Nickname
			u.Props = in.Props
			return u.DeepCopy(), nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// UpdateUserActive implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) UpdateUserActive(userID string, active bool) *model.AppError {
	for _, u := range f.Users {
		if u.Id == userID {
			if active {
				u.DeleteAt = 0
			} else {
				u.DeleteAt = 1000
			}
			return nil
		}
	}
	return model.NewAppError("test", "test", nil, "no such user", 404)
}

// UpdateUserAuth implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) UpdateUserAuth(userID string, auth *model.UserAuth) (*model.UserAuth, *model.AppError) {
	for _, u := range f.Users {
		if u.Id == userID {
			u.AuthService = auth.AuthService
			u.AuthData = auth.AuthData
			return &model.UserAuth{
				AuthService: u.AuthService,
				AuthData:    u.AuthData,
			}, nil
		}
	}
	return nil, model.NewAppError("test", "test", nil, "no such user", 404)
}

// CreateSession implements mattermostPluginAPI.
func (f *fakeMattermostPluginAPI) CreateSession(session *model.Session) (*model.Session, *model.AppError) {
	panic("unimplemented")
}

var _ mattermostPluginAPI = (*fakeMattermostPluginAPI)(nil)

func makeService(p *fakeMattermostPluginAPI) *MattermostService {
	return &MattermostService{
		API: p,
	}
}

func TestMattermostFetchUsers(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:          "id::user::testuser",
			Username:    "testuser",
			Nickname:    "Test User",
			Email:       "testuser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1000",
			},
		}, {
			Id:          "id::user::disableduser",
			Username:    "disableduser",
			Nickname:    "Disabled User",
			Email:       "disableduser@example.com",
			AuthService: "openid",
			Props: map[string]string{
				MMIdPUserIDProp: "1001",
			},
			DeleteAt: 1000,
		}, {
			Id:          "id::user::systemuser",
			Username:    "systemuser",
			Nickname:    "System User",
			Email:       "systemuser@example.com",
			AuthService: "password",
		}},
	}
	m := makeService(p)

	want := []*User[string]{{
		UserID:        "id::user::testuser",
		Username:      "testuser",
		DisplayName:   "Test User",
		Email:         "testuser@example.com",
		Active:        true,
		IDPUserID:     1000,
		ServiceUserID: "id::user::testuser",
		ServiceUser:   p.Users[0],
	}, {
		UserID:        "id::user::disableduser",
		Username:      "disableduser",
		DisplayName:   "Disabled User",
		Email:         "disableduser@example.com",
		Active:        false,
		IDPUserID:     1001,
		ServiceUserID: "id::user::disableduser",
		ServiceUser:   p.Users[1],
	}}

	got, err := m.FetchUsers(context.Background())
	if err != nil {
		t.Fatalf("FetchUsers: %v", err)
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("FetchUsers diff (-got +want):\n%s", diff)
	}
}

func ptr[T any](t T) *T { return &t }

func TestMattermostCreateUsersThatAreUnassociated(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:          "some::nonmatch::id",
			Username:    "some-other-username",
			Nickname:    "Some Old Nickname",
			Email:       "testuser@example.com",
			AuthService: "email",
		}},
	}
	m := makeService(p)

	got, err := m.CreateUsers(context.Background(), []*User[int]{{
		UserID:      1000,
		Username:    "testuser",
		DisplayName: "Test User",
		Email:       "testuser@example.com",
		Active:      true,
	}})
	if err != nil {
		t.Fatalf("CreateUsers: %v", err)
	}

	want := []*User[string]{{
		UserID:      "some::nonmatch::id",
		Username:    "testuser",
		DisplayName: "Test User",
		Email:       "testuser@example.com",
		Active:      true,

		IDPUserID:     1000,
		ServiceUserID: "some::nonmatch::id",
		ServiceUser: &model.User{
			Id:          "some::nonmatch::id",
			Username:    "testuser",
			Nickname:    "Test User",
			AuthData:    ptr("1000"),
			AuthService: "openid",
			Email:       "testuser@example.com",
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "testuser",
			},
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("CreateUsers diff (-got +want):\n%s", diff)
	}
}

func TestMattermostCreateUsers(t *testing.T) {
	p := &fakeMattermostPluginAPI{}
	m := makeService(p)

	got, err := m.CreateUsers(context.Background(), []*User[int]{{
		UserID:      1000,
		Username:    "testuser",
		DisplayName: "Test User",
		Email:       "testuser@example.com",
		Active:      true,
	}, {
		UserID:      1001,
		Username:    "disableduser",
		DisplayName: "Disabled User",
		Email:       "disableduser@example.com",
		Active:      false,
	}})
	if err != nil {
		t.Fatalf("CreateUsers: %v", err)
	}

	want := []*User[string]{{
		UserID:      "id::user::testuser",
		Username:    "testuser",
		DisplayName: "Test User",
		Email:       "testuser@example.com",
		Active:      true,

		IDPUserID:     1000,
		ServiceUserID: "id::user::testuser",
		ServiceUser: &model.User{
			Id:            "id::user::testuser",
			Username:      "testuser",
			Nickname:      "Test User",
			AuthData:      ptr("1000"),
			AuthService:   "openid",
			Email:         "testuser@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "testuser",
			},
			DisableWelcomeEmail: true,
		},
	}, {
		UserID:      "id::user::disableduser",
		Username:    "disableduser",
		DisplayName: "Disabled User",
		Email:       "disableduser@example.com",
		Active:      false,

		IDPUserID:     1001,
		ServiceUserID: "id::user::disableduser",
		ServiceUser: &model.User{
			Id:            "id::user::disableduser",
			Username:      "disableduser",
			Nickname:      "Disabled User",
			AuthData:      ptr("1001"),
			AuthService:   "openid",
			Email:         "disableduser@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1001",
				MMIdPUsernameProp: "disableduser",
			},
			DisableWelcomeEmail: true,
			DeleteAt:            1000,
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("CreateUsers diff (-got +want):\n%s", diff)
	}
}

func TestMattermostUpdateUsers(t *testing.T) {
	p := &fakeMattermostPluginAPI{
		Users: []*model.User{{
			Id:            "id::user::testuser",
			Username:      "testuser",
			Nickname:      "Test User",
			AuthService:   "openid",
			Email:         "testuser@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "testuser",
			},
			DisableWelcomeEmail: true,
		}, {
			Id:            "id::user::testuser2",
			Username:      "testuser2",
			Nickname:      "Test User 2",
			AuthService:   "openid",
			Email:         "testuser2@example.com",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1001",
				MMIdPUsernameProp: "testuser2",
			},
			DisableWelcomeEmail: true,
		}, {
			Id:            "id::user::disableduser",
			Username:      "disableduser",
			Nickname:      "Reenabled User",
			AuthService:   "openid",
			Email:         "disableduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1002",
				MMIdPUsernameProp: "disableduser",
			},
			DisableWelcomeEmail: true,
			DeleteAt:            1000,
		}, {
			Id:            "id::user::untoucheduser",
			Username:      "untoucheduser",
			Nickname:      "Untouched User",
			AuthService:   "openid",
			Email:         "untoucheduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1003",
				MMIdPUsernameProp: "untoucheduser",
			},
			DisableWelcomeEmail: true,
		}},
	}
	m := makeService(p)

	got, updated, err := m.UpdateUsers(context.Background(), []*User[string]{{
		UserID:      "id::user::testuser",
		Username:    "nowdisabled",
		DisplayName: "New Display Name",
		Email:       "newemail@example.bin",
		Active:      false,
	}, {
		UserID:      "id::user::testuser2",
		Username:    "newuser2",
		DisplayName: "New Display Name 2",
		Email:       "newemail2@example.bin",
		Active:      true,
	}, {
		UserID:      "id::user::disableduser",
		Username:    "disableduser",
		DisplayName: "Reenabled User",
		Email:       "disableduser@example.bin",
		Active:      true,
	}, {
		UserID:      "id::user::untoucheduser",
		Username:    "untoucheduser",
		DisplayName: "Untouched User",
		Email:       "untoucheduser@example.bin",
		Active:      true,
	}})
	if err != nil {
		t.Fatalf("UpdateUsers: %v", err)
	}

	want := []*User[string]{{
		UserID:      "id::user::testuser",
		Username:    "nowdisabled",
		DisplayName: "New Display Name",
		Email:       "newemail@example.bin",
		Active:      false,

		IDPUserID:     1000,
		ServiceUserID: "id::user::testuser",
		ServiceUser: &model.User{
			Id:            "id::user::testuser",
			Username:      "nowdisabled",
			Nickname:      "New Display Name",
			AuthService:   "openid",
			Email:         "newemail@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1000",
				MMIdPUsernameProp: "nowdisabled",
			},
			DisableWelcomeEmail: true,
			DeleteAt:            1000,
		},
	}, {
		UserID:      "id::user::testuser2",
		Username:    "newuser2",
		DisplayName: "New Display Name 2",
		Email:       "newemail2@example.bin",
		Active:      true,

		IDPUserID:     1001,
		ServiceUserID: "id::user::testuser2",
		ServiceUser: &model.User{
			Id:            "id::user::testuser2",
			Username:      "newuser2",
			Nickname:      "New Display Name 2",
			AuthService:   "openid",
			Email:         "newemail2@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1001",
				MMIdPUsernameProp: "newuser2",
			},
			DisableWelcomeEmail: true,
		},
	}, {
		UserID:      "id::user::disableduser",
		Username:    "disableduser",
		DisplayName: "Reenabled User",
		Email:       "disableduser@example.bin",
		Active:      true,

		IDPUserID:     1002,
		ServiceUserID: "id::user::disableduser",
		ServiceUser: &model.User{
			Id:            "id::user::disableduser",
			Username:      "disableduser",
			Nickname:      "Reenabled User",
			AuthService:   "openid",
			Email:         "disableduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1002",
				MMIdPUsernameProp: "disableduser",
			},
			DisableWelcomeEmail: true,
		},
	}, {
		UserID:      "id::user::untoucheduser",
		Username:    "untoucheduser",
		DisplayName: "Untouched User",
		Email:       "untoucheduser@example.bin",
		Active:      true,

		IDPUserID:     1003,
		ServiceUserID: "id::user::untoucheduser",
		ServiceUser: &model.User{
			Id:            "id::user::untoucheduser",
			Username:      "untoucheduser",
			Nickname:      "Untouched User",
			AuthService:   "openid",
			Email:         "untoucheduser@example.bin",
			EmailVerified: true,
			Props: map[string]string{
				MMIdPUserIDProp:   "1003",
				MMIdPUsernameProp: "untoucheduser",
			},
			DisableWelcomeEmail: true,
		},
	}}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("UpdateUsers diff (-got +want):\n%s", diff)
	}

	wantUpdated := []bool{
		true, true, true, false,
	}
	if diff := cmp.Diff(updated, wantUpdated); diff != "" {
		t.Fatalf("UpdateUsers updated diff (-got +want):\n%s", diff)
	}
}
