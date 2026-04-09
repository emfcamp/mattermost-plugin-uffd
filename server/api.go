package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/lukegb/mattermost-plugin-uffd/server/ctxlog"
)

// ServeHTTP demonstrates a plugin that handles HTTP requests by greeting the world.
// The root URL is currently <siteUrl>/plugins/org.emfcamp.mattermost-plugin-uffd/api/v1/.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	router := mux.NewRouter()

	router.HandleFunc("/login", p.HTTPSyncThenLogin).Methods(http.MethodGet)

	apiRouter := router.PathPrefix("/api/v1").Subrouter()
	apiRouter.Use(p.MattermostAuthorizationRequired)
	// No particular permissions are required to force a sync (at the moment...)
	apiRouter.HandleFunc("/sync", p.HTTPSyncNow).Methods(http.MethodPost)
	apiRouter.HandleFunc("/userdataspec", p.UserDataSpec).Methods(http.MethodGet)
	apiRouter.HandleFunc("/userdata/{userid}", p.UserData).Methods(http.MethodGet)
	p.registerDialogHandlers(apiRouter.PathPrefix("/dialog").Subrouter())

	router.ServeHTTP(w, r)
}

func (p *Plugin) MattermostAuthorizationRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("Mattermost-User-ID")
		if userID == "" {
			http.Error(w, "Not authorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// e.g. http://localhost:8065/plugins/org.emfcamp.mattermost-plugin-uffd/login
func (p *Plugin) HTTPSyncThenLogin(w http.ResponseWriter, r *http.Request) {
	if err := p.runSync(context.WithoutCancel(r.Context()), "HTTP /login"); err != nil {
		http.Redirect(w, r, "/error", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/oauth/openid/login", http.StatusSeeOther)
}

func (p *Plugin) HTTPSyncNow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")

	if err := p.runSync(context.WithoutCancel(r.Context()), "HTTP /sync"); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
	} else {
		// No error.
		w.WriteHeader(http.StatusNoContent)
	}
}

func (p *Plugin) UserDataSpec(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l := ctxlog.FromContext(ctx)

	teams, err := p.datastore().LoadTeams(ctx)
	if err != nil {
		l.WithError(err).Error("loading teams serving UserData request")
		http.Error(w, "Internal server error loading teams", http.StatusInternalServerError)
		return
	}
	teamOptions := make(model.PropertyOptions[*model.CustomProfileAttributesSelectOption], len(teams))
	for n, team := range teams {
		teamOptions[n] = &model.CustomProfileAttributesSelectOption{
			ID:   team.Name,
			Name: team.Name,
		}
	}
	slices.SortFunc(teamOptions, func(a, b *model.CustomProfileAttributesSelectOption) int {
		return strings.Compare(a.ID, b.ID)
	})

	var fields []model.CPAField
	fields = append(fields, model.CPAField{
		PropertyField: model.PropertyField{
			ID:        "emf_teamlead",
			GroupID:   "emf_teamlead",
			Name:      "Team Lead",
			Type:      model.PropertyFieldTypeMultiselect,
			Protected: true,
		},
		Attrs: model.CPAAttrs{
			Visibility:     model.CustomProfileAttributesVisibilityWhenSet,
			SortOrder:      1.0,
			Options:        teamOptions,
			ValueType:      "",
			LDAP:           "",
			SAML:           "",
			Managed:        "admin",
			Protected:      true,
			SourcePluginID: "emfcamp",
			AccessMode:     "protected",
		},
	})
	fields = append(fields, model.CPAField{
		PropertyField: model.PropertyField{
			ID:        "emf_teammember",
			GroupID:   "emf_teammember",
			Name:      "Team Member",
			Type:      model.PropertyFieldTypeMultiselect,
			Protected: true,
		},
		Attrs: model.CPAAttrs{
			Visibility:     model.CustomProfileAttributesVisibilityWhenSet,
			SortOrder:      2.0,
			Options:        teamOptions,
			ValueType:      "",
			LDAP:           "",
			SAML:           "",
			Managed:        "admin",
			Protected:      true,
			SourcePluginID: "emfcamp",
			AccessMode:     "protected",
		},
	})
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(fields); err != nil {
		l.WithError(err).Error("encoding and returning user property metadata")
		// It's too late to return a proper error to the user...
	}
}

type userDataResponse struct {
	TeamLead   []string `json:"emf_teamlead"`
	TeamMember []string `json:"emf_teammember"`
}

func (p *Plugin) UserData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	l := ctxlog.FromContext(ctx)

	vars := mux.Vars(r)
	userID := vars["userid"]
	if userID == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	teams, err := p.datastore().LoadTeams(ctx)
	if err != nil {
		l.WithError(err).Error("loading teams serving UserData request")
		http.Error(w, "Internal server error loading teams", http.StatusInternalServerError)
		return
	}

	var out userDataResponse
	for _, team := range teams {
		switch {
		case slices.Contains(team.Leads, userID):
			out.TeamLead = append(out.TeamLead, team.Name)
		case slices.Contains(team.Members, userID):
			out.TeamMember = append(out.TeamMember, team.Name)
		}
	}
	slices.Sort(out.TeamLead)
	slices.Sort(out.TeamMember)

	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		l.WithError(err).Error("encoding and returning user data")
		// It's too late to return a proper error to the user...
	}
}
