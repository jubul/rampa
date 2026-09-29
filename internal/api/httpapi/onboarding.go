package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"rampa.dev/internal/api/store"
)

// onboardingStep es un paso del onboarding guiado (docs/api.md).
type onboardingStep struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Done   bool   `json:"done"`
	Detail string `json:"detail"`
}

type onboardingResponse struct {
	Steps    []onboardingStep `json:"steps"`
	Next     string           `json:"next"`
	Complete bool             `json:"complete"`
}

// onboarding calcula el estado de los cinco pasos: cuenta connected, algún
// cluster, algún cluster online, alguna application y algún deployment healthy.
func (a *API) onboarding(w http.ResponseWriter, r *http.Request) {
	oc := orgCtx(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	accounts, err := a.Store.ListCloudAccounts(ctx, oc.Org.ID)
	if err != nil {
		a.fail(w, r, err, "cuentas de nube")
		return
	}
	clusters, err := a.Store.ListClusters(ctx, oc.Org.ID, "")
	if err != nil {
		a.fail(w, r, err, "clusters")
		return
	}
	apps, err := a.Store.ListApplications(ctx, oc.Org.ID)
	if err != nil {
		a.fail(w, r, err, "applications")
		return
	}
	healthy, err := a.Store.LatestDeploymentWithStatus(ctx, oc.Org.ID, store.DeploymentHealthy)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.fail(w, r, err, "deployments")
		return
	}

	cloud := onboardingStep{Key: "cloud", Title: "Conectar una cuenta de nube"}
	for i := range accounts {
		acc := &accounts[i]
		if acc.Status == store.CloudAccountConnected {
			cloud.Done = true
			cloud.Detail = fmt.Sprintf("%s (%s, %s)", acc.Name, acc.Provider, acc.Status)
			break
		}
	}
	if !cloud.Done && len(accounts) > 0 {
		cloud.Detail = fmt.Sprintf("%s (%s, %s)", accounts[0].Name, accounts[0].Provider, accounts[0].Status)
	}

	cluster := onboardingStep{Key: "cluster", Title: "Registrar o crear un cluster"}
	agent := onboardingStep{Key: "agent", Title: "Instalar el agente"}
	if len(clusters) > 0 {
		cluster.Done = true
		names := make([]string, 0, len(clusters))
		for _, cl := range clusters {
			names = append(names, cl.Name)
		}
		cluster.Detail = strings.Join(names, ", ")
		for _, cl := range clusters {
			if cl.Status == store.ClusterOnline {
				agent.Done = true
				agent.Detail = cl.Name + " está online"
				break
			}
		}
		if !agent.Done {
			agent.Detail = clusters[0].Name + " está " + clusters[0].Status
		}
	}

	app := onboardingStep{Key: "app", Title: "Crear la primera aplicación"}
	if len(apps) > 0 {
		app.Done = true
		names := make([]string, 0, len(apps))
		for _, ap := range apps {
			names = append(names, ap.Name)
		}
		app.Detail = strings.Join(names, ", ")
	}

	deploy := onboardingStep{Key: "deploy", Title: "Hacer el primer deploy"}
	if healthy != nil {
		deploy.Done = true
		deploy.Detail = healthy.AppName + "/" + healthy.ScopeName + " healthy"
	}

	resp := onboardingResponse{Steps: []onboardingStep{cloud, cluster, agent, app, deploy}, Complete: true}
	for _, s := range resp.Steps {
		if !s.Done {
			resp.Next = s.Key
			resp.Complete = false
			break
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
