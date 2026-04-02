package main

import (
	"context"
	"fmt"
	"log"
	"sort"

	miot "github.com/ysh0566/go-mihome"
	"github.com/ysh0566/go-mihome/example/internal/exampleutil"
)

const (
	defaultClientID    = "2882303761520431603"
	defaultCloudServer = "cn"
)

type roomSummary struct {
	RoomID      string   `json:"room_id"`
	RoomName    string   `json:"room_name"`
	DeviceCount int      `json:"device_count"`
	DIDs        []string `json:"dids"`
}

type homeSummary struct {
	HomeID      string        `json:"home_id"`
	HomeName    string        `json:"home_name"`
	UID         string        `json:"uid"`
	GroupID     string        `json:"group_id"`
	DeviceCount int           `json:"device_count"`
	DIDs        []string      `json:"dids"`
	Rooms       []roomSummary `json:"rooms"`
}

func main() {
	log.SetFlags(0)

	cfg, err := exampleutil.LoadCloudConfig(exampleutil.CloudConfig{
		ClientID:    defaultClientID,
		CloudServer: defaultCloudServer,
	})
	if err != nil {
		log.Fatal(err)
	}

	client, err := cfg.NewCloudClient()
	if err != nil {
		log.Fatal(err)
	}

	homes, err := client.GetHomeInfos(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	devices, err := client.GetDevices(context.Background(), []string{"demo-home-device-1", "demo-home-device-2"})
	if err != nil {
		log.Fatal(err)
	}
	if err := exampleutil.PrintJSONStdout(struct {
		UID         string        `json:"uid"`
		Homes       []homeSummary `json:"homes"`
		SharedHomes []homeSummary `json:"shared_homes"`
	}{
		UID:         homes.UID,
		Homes:       summarizeHomes(homes.HomeList),
		SharedHomes: summarizeHomes(homes.ShareHomeList),
	}); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("devices: %+v\n", devices)
}

func summarizeHomes(items map[string]miot.HomeInfo) []homeSummary {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	homes := make([]homeSummary, 0, len(keys))
	for _, key := range keys {
		home := items[key]
		roomKeys := make([]string, 0, len(home.Rooms))
		for roomID := range home.Rooms {
			roomKeys = append(roomKeys, roomID)
		}
		sort.Strings(roomKeys)

		rooms := make([]roomSummary, 0, len(roomKeys))
		for _, roomID := range roomKeys {
			room := home.Rooms[roomID]
			dids := append([]string(nil), room.DIDs...)
			sort.Strings(dids)
			rooms = append(rooms, roomSummary{
				RoomID:      room.RoomID,
				RoomName:    room.RoomName,
				DeviceCount: len(dids),
				DIDs:        dids,
			})
		}

		dids := append([]string(nil), home.DIDs...)
		sort.Strings(dids)
		homes = append(homes, homeSummary{
			HomeID:      home.HomeID,
			HomeName:    home.HomeName,
			UID:         home.UID,
			GroupID:     home.GroupID,
			DeviceCount: len(dids),
			DIDs:        dids,
			Rooms:       rooms,
		})
	}
	return homes
}
