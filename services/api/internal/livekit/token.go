package livekit

import (
	"time"

	"github.com/livekit/protocol/auth"
)

type TokenGenerator struct {
	apiKey    string
	apiSecret string
}

func NewTokenGenerator(apiKey, apiSecret string) *TokenGenerator {
	return &TokenGenerator{
		apiKey:    apiKey,
		apiSecret: apiSecret,
	}
}

type JoinTokenRequest struct {
	Identity string `json:"identity"`
}

type JoinTokenResponse struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Room  string `json:"room"`
}

func (tg *TokenGenerator) GenerateJoinToken(identity string) (*JoinTokenResponse, error) {
	// 1時間有効なトークンを生成
	at := auth.NewAccessToken(tg.apiKey, tg.apiSecret)
	canPublish := false
	canSubscribe := true
	grant := &auth.VideoGrant{
		RoomJoin:     true,
		Room:         "radio-24",
		CanPublish:   &canPublish, // Subscribe only
		CanSubscribe: &canSubscribe,
	}
	at.AddGrant(grant).
		SetIdentity(identity).
		SetValidFor(time.Hour)

	token, err := at.ToJWT()
	if err != nil {
		return nil, err
	}

	return &JoinTokenResponse{
		URL:   "ws://localhost:7880", // ローカル開発用
		Token: token,
		Room:  "radio-24",
	}, nil
}
