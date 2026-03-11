package domain

import (
	"testing"
	"time"
)

func TestValidateGatewayEndpoint(t *testing.T) {
	t.Parallel()

	err := ValidateGatewayEndpoint(GatewayEndpoint{
		ID:             "gw_1",
		Type:           GatewayEndpointTypeTelegram,
		Enabled:        true,
		Target:         "chat:1",
		AllowedActions: []RemoteCommandAction{RemoteCommandActionApprove, RemoteCommandActionReject},
	})
	if err != nil {
		t.Fatalf("ValidateGatewayEndpoint() error = %v", err)
	}
}

func TestValidateRemoteCommandRejectsUnknownAction(t *testing.T) {
	t.Parallel()

	err := ValidateRemoteCommand(RemoteCommand{
		CommandID:        "rcmd_1",
		SourceEndpointID: "gw_1",
		Actor:            "user:leo",
		SessionID:        "sess_1",
		Action:           RemoteCommandAction("shell"),
		IssuedAt:         time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("ValidateRemoteCommand() error = nil, want error")
	}
}
