package uffd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type API struct {
	HTTPClient *http.Client

	EndpointBase string
	Username     string
	Password     string
}

func (u *API) endpoint(path string) string {
	return strings.TrimRight(u.EndpointBase, "/") + path
}

func ensureNoTrailingContent(r io.Reader) error {
	b := make([]byte, 10)
	_, err := r.Read(b)
	if err != io.EOF {
		return fmt.Errorf("trailing content")
	}
	return nil
}

func (u *API) do(ctx context.Context, method string, path string, body io.Reader, decode any) error {
	req, err := http.NewRequestWithContext(ctx, method, u.endpoint(path), body)
	if err != nil {
		return fmt.Errorf("creating HTTP request: %w", err)
	}
	req.SetBasicAuth(u.Username, u.Password)

	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("bad HTTP response code %d (body could not be read)", resp.StatusCode)
		}
		return fmt.Errorf("bad HTTP response code %d - body: %v", resp.StatusCode, string(errBody))
	}

	// TODO(lukegb): check application/json in Content-Type?
	if err := json.NewDecoder(resp.Body).Decode(decode); err != nil {
		return fmt.Errorf("parsing JSON response: %w", err)
	}

	if err := ensureNoTrailingContent(resp.Body); err != nil {
		return err
	}

	return nil
}

type Group struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

func (u *API) GetGroups(ctx context.Context) ([]Group, error) {
	var groups []Group
	if err := u.do(ctx, "GET", "/api/v1/getgroups", nil, &groups); err != nil {
		return nil, fmt.Errorf("getgroups: %w", err)
	}
	return groups, nil
}

type User struct {
	DisplayName string   `json:"displayname"`
	Email       string   `json:"email"`
	Groups      []string `json:"groups"`
	ID          int      `json:"id"`
	LoginName   string   `json:"loginname"`
}

func (u *API) GetUsers(ctx context.Context) ([]User, error) {
	var users []User
	if err := u.do(ctx, "GET", "/api/v1/getusers", nil, &users); err != nil {
		return nil, fmt.Errorf("getusers: %w", err)
	}
	return users, nil
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }

var ErrNotFound = notFoundErr{}

type tooManyResultsErr struct{}

func (tooManyResultsErr) Error() string { return "too many results" }

var ErrTooManyResults = tooManyResultsErr{}

func (u *API) getSingleUser(ctx context.Context, queryString url.Values) (*User, error) {
	var users []User
	if err := u.do(ctx, "GET", "/api/v1/getusers?"+queryString.Encode(), nil, &users); err != nil {
		return nil, fmt.Errorf("getusers: %w", err)
	}
	switch {
	case len(users) == 1:
		return &users[0], nil
	case len(users) > 1:
		return nil, ErrTooManyResults
	default:
		return nil, ErrNotFound
	}
}

func (u *API) GetUserByID(ctx context.Context, id int) (*User, error) {
	return u.getSingleUser(ctx, url.Values{"id": []string{strconv.Itoa(id)}})
}
