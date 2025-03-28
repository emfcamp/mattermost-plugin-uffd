package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// ServeHTTP demonstrates a plugin that handles HTTP requests by greeting the world.
// The root URL is currently <siteUrl>/plugins/org.emfcamp.mattermost-plugin-uffd/api/v1/.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	router := mux.NewRouter()

	router.HandleFunc("/login", p.HttpSyncThenLogin).Methods(http.MethodGet)

	apiRouter := router.PathPrefix("/api/v1").Subrouter()
	apiRouter.Use(p.MattermostAuthorizationRequired)
	// No particular permissions are required to force a sync (at the moment...)
	apiRouter.HandleFunc("/sync", p.HttpSyncNow).Methods(http.MethodPost)

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
func (p *Plugin) HttpSyncThenLogin(w http.ResponseWriter, r *http.Request) {
	if err := p.runSync("HTTP /login"); err != nil {
		http.Redirect(w, r, "/error", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/oauth/openid/login", http.StatusSeeOther)
}

func (p *Plugin) HttpSyncNow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")

	if err := p.runSync("HTTP /sync"); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s\n", err)
	} else {
		// No error.
		w.WriteHeader(http.StatusNoContent)
	}
}
