package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sort"
	"time"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const defaultDiscoveryTimeout = 15 * time.Second

func main() {
	log.SetFlags(0)

	groupID := exampleutil.LookupString("MIOT_MIPS_GROUP_ID", "3ca66192999f0c3e")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, defaultDiscoveryTimeout)
	defer cancel()

	discovery := miot.NewMIPSDiscovery(nil)
	sub := discovery.SubscribeServiceChange(groupID, func(event miot.MIPSServiceEvent) {
		_ = exampleutil.PrintJSONStdout(struct {
			Type    string               `json:"type"`
			GroupID string               `json:"group_id"`
			Service miot.MIPSServiceInfo `json:"service"`
		}{
			Type:    string(event.State),
			GroupID: event.GroupID,
			Service: event.Service,
		})
	})
	defer sub.Close()

	if err := discovery.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer discovery.Close()

	<-ctx.Done()

	if err := exampleutil.PrintJSONStdout(struct {
		GroupFilter string                 `json:"group_filter,omitempty"`
		Services    []miot.MIPSServiceInfo `json:"services"`
	}{
		GroupFilter: groupID,
		Services:    snapshotServices(discovery.Services(), groupID),
	}); err != nil {
		log.Fatal(err)
	}
}

func snapshotServices(items map[string]miot.MIPSServiceInfo, groupID string) []miot.MIPSServiceInfo {
	keys := make([]string, 0, len(items))
	for key, info := range items {
		if groupID != "" && info.GroupID != groupID {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	services := make([]miot.MIPSServiceInfo, 0, len(keys))
	for _, key := range keys {
		services = append(services, items[key])
	}
	return services
}
