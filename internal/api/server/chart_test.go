package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"helm.sh/helm/v3/pkg/chart/loader"

	"rampa.dev/charts"
)

// TestPackageChartLoadsWithHelm: el tgz que sirve la API se carga con el SDK
// de Helm (lo mismo que hace helm install <url>) y es el chart rampa-agent.
func TestPackageChartLoadsWithHelm(t *testing.T) {
	tgz, err := PackageChart(charts.RampaAgent, "rampa-agent")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := loader.LoadArchive(bytes.NewReader(tgz))
	if err != nil {
		t.Fatalf("LoadArchive: %v", err)
	}
	if ch.Name() != "rampa-agent" || ch.Metadata.Version == "" {
		t.Errorf("chart: %s %s", ch.Name(), ch.Metadata.Version)
	}
	templates := map[string]bool{}
	for _, f := range ch.Templates {
		templates[f.Name] = true
	}
	for _, want := range []string{"templates/deployment.yaml", "templates/secret.yaml", "templates/serviceaccount.yaml"} {
		if !templates[want] {
			t.Errorf("falta %s en el chart empaquetado: %v", want, templates)
		}
	}
	if ch.Values["api"] == nil {
		t.Errorf("values.yaml no se cargó: %v", ch.Values)
	}
	// Reproducible: dos empaquetados dan los mismos bytes.
	again, _ := PackageChart(charts.RampaAgent, "rampa-agent")
	if !bytes.Equal(tgz, again) {
		t.Error("el tgz debería ser reproducible")
	}
	if _, err := PackageChart(charts.RampaAgent, "no-existe"); err == nil {
		t.Error("un chart inexistente debería fallar")
	}

	rec := httptest.NewRecorder()
	serveAgentChart(rec, httptest.NewRequest(http.MethodGet, AgentChartPath, nil), tgz)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" || rec.Header().Get("Cache-Control") != "public, max-age=300" || rec.Body.Len() != len(tgz) {
		t.Errorf("respuesta: %d %v %d bytes", rec.Code, rec.Header(), rec.Body.Len())
	}
	rec = httptest.NewRecorder()
	serveAgentChart(rec, httptest.NewRequest(http.MethodPost, AgentChartPath, nil), tgz)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST debería ser 405: %d", rec.Code)
	}
}
