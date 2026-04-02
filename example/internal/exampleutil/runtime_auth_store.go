package exampleutil

import (
	"context"
	"strings"

	miot "github.com/ysh0566/go-mihome"
)

// RuntimeOAuthTokenStore scopes runtime auth_info entries by OAuth client id.
type RuntimeOAuthTokenStore struct {
	storage     *miot.Storage
	uid         string
	cloudServer string
	clientID    string
}

// NewRuntimeOAuthTokenStore creates an MIoT auth store scoped to one runtime OAuth client id.
func NewRuntimeOAuthTokenStore(storage *miot.Storage, uid, cloudServer, clientID string) miot.MIoTAuthStore {
	return RuntimeOAuthTokenStore{
		storage:     storage,
		uid:         uid,
		cloudServer: cloudServer,
		clientID:    strings.TrimSpace(clientID),
	}
}

func (s RuntimeOAuthTokenStore) LoadOAuthToken(ctx context.Context) (miot.OAuthToken, error) {
	if s.storage == nil {
		return miot.OAuthToken{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "load runtime oauth token", Msg: "storage is nil"}
	}
	key := runtimeAuthInfoKey(s.clientID)
	doc, err := s.storage.LoadUserConfig(ctx, s.uid, s.cloudServer, key)
	if err != nil {
		return miot.OAuthToken{}, err
	}
	for _, entry := range doc.Entries {
		if entry.Key == key {
			return miot.DecodeUserConfigEntry[miot.OAuthToken](entry)
		}
	}
	return miot.OAuthToken{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "load runtime oauth token", Msg: key + " entry not found"}
}

func (s RuntimeOAuthTokenStore) SaveOAuthToken(ctx context.Context, token miot.OAuthToken) error {
	if s.storage == nil {
		return &miot.Error{Code: miot.ErrInvalidArgument, Op: "save runtime oauth token", Msg: "storage is nil"}
	}
	entry, err := miot.NewUserConfigEntry(runtimeAuthInfoKey(s.clientID), token)
	if err != nil {
		return err
	}
	return s.storage.UpdateUserConfig(ctx, s.uid, s.cloudServer, &miot.UserConfigDocument{
		Entries: []miot.UserConfigEntry{entry},
	}, false)
}

func runtimeAuthInfoKey(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return "auth_info"
	}
	return "auth_info_" + clientID
}
