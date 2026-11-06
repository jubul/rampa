package infra

import (
	"strings"
	"testing"
)

func TestAgentReachability(t *testing.T) {
	cases := []struct {
		url, image string
		ok         bool
		want       string
	}{
		{"https://api.rampa.example.com", "ghcr.io/acme/rampa-agent:0.1.0", true, ""},
		{"http://localhost:8080", "ghcr.io/acme/rampa-agent:0.1.0", false, "RAMPA_PUBLIC_URL"},
		{"http://127.0.0.1:8080", "ghcr.io/acme/rampa-agent:0.1.0", false, "RAMPA_PUBLIC_URL"},
		{"http://192.168.1.10:8080", "ghcr.io/acme/rampa-agent:0.1.0", false, "RAMPA_PUBLIC_URL"},
		{"https://api.rampa.example.com", "ghcr.io/CHANGEME/rampa-agent:latest", false, "RAMPA_AGENT_IMAGE"},
	}
	for _, c := range cases {
		s := &Service{cfg: Config{PublicURL: c.url, AgentImage: c.image}}
		err := s.AgentReachability("en")
		if (err == nil) != c.ok || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s %s: %v", c.url, c.image, err)
		}
	}
}
