package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type UffdAPI struct {
	HTTPClient *http.Client

	EndpointBase string
	Username     string
	Password     string
}

func (u *UffdAPI) endpoint(path string) string {
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

func (u *UffdAPI) do(ctx context.Context, method string, path string, body io.Reader, decode any) error {
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

type UffdGroup struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

func (u *UffdAPI) GetGroups(ctx context.Context) ([]UffdGroup, error) {
	var groups []UffdGroup
	if err := u.do(ctx, "GET", "/api/v1/getgroups", nil, &groups); err != nil {
		return nil, fmt.Errorf("getgroups: %w", err)
	}
	return groups, nil
}

type UffdUser struct {
	DisplayName string   `json:"displayname"`
	Email       string   `json:"email"`
	Groups      []string `json:"groups"`
	ID          int      `json:"id"`
	LoginName   string   `json:"loginname"`
}

func (u *UffdAPI) GetUsers(ctx context.Context) ([]UffdUser, error) {
	var users []UffdUser
	if err := u.do(ctx, "GET", "/api/v1/getusers", nil, &users); err != nil {
		return nil, fmt.Errorf("getusers: %w", err)
	}
	return users, nil
}
