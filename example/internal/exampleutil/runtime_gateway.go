package exampleutil

import (
	"strings"

	miot "github.com/ysh0566/go-mihome"
)

// RuntimeLocalRouteCandidate is a pure-data description of one local-gateway route option.
type RuntimeLocalRouteCandidate struct {
	HomeID    string
	HomeName  string
	GroupID   string
	ClientDID string
	Host      string
	Port      int
}

// BuildRuntimeLocalRouteCandidates builds deterministic gateway route candidates from selected homes and discovered services.
func BuildRuntimeLocalRouteCandidates(
	homes []miot.MIoTClientHome,
	services map[string]miot.MIPSServiceInfo,
	runtimeDID string,
) []RuntimeLocalRouteCandidate {
	candidates := make([]RuntimeLocalRouteCandidate, 0, len(homes))
	for _, home := range homes {
		service, ok := services[home.GroupID]
		if !ok || service.GroupID != home.GroupID {
			continue
		}

		candidate := RuntimeLocalRouteCandidate{
			HomeID:    home.HomeID,
			HomeName:  home.HomeName,
			GroupID:   home.GroupID,
			ClientDID: runtimeDID,
			Host:      primaryRuntimeServiceHost(service.Addresses),
			Port:      service.Port,
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func primaryRuntimeServiceHost(addresses []string) string {
	for _, address := range addresses {
		if host := strings.TrimSpace(address); host != "" {
			return host
		}
	}
	return ""
}
