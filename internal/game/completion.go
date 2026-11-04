package game

import (
	"context"
	"time"
)

type ParticipantResult struct {
	PlayerID     string `json:"player_id"`
	TeamID       string `json:"team_id,omitempty"`
	Health       int    `json:"health"`
	Alive        bool   `json:"alive"`
	Knocked      bool   `json:"knocked_down"`
	Spectator    bool   `json:"spectator"`
	Eliminations int    `json:"eliminations"`
}

type Completion struct {
	SessionID    string              `json:"session_id"`
	Reason       string              `json:"reason"`
	WinnerID     string              `json:"winner_id,omitempty"`
	CompletedAt  time.Time           `json:"completed_at"`
	Participants []ParticipantResult `json:"participants"`
}

type CompletionSink interface {
	RecordGameCompletion(context.Context, Completion) error
}
