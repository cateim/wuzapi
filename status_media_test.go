package main

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestIsStatusChat(t *testing.T) {
	cases := []struct {
		name string
		chat types.JID
		want bool
	}{
		{"status feed", types.NewJID("status", types.BroadcastServer), true},
		{"status feed parsed", mustParseJID(t, "status@broadcast"), true},
		{"broadcast list", mustParseJID(t, "1699999999@broadcast"), false},
		{"group", mustParseJID(t, "120363@g.us"), false},
		{"direct chat", mustParseJID(t, "5511999999999@s.whatsapp.net"), false},
		{"lid chat", mustParseJID(t, "123456789@lid"), false},
		{"empty", types.JID{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStatusChat(tc.chat); got != tc.want {
				t.Fatalf("isStatusChat(%q) = %v, want %v", tc.chat.String(), got, tc.want)
			}
		})
	}
}

func mustParseJID(t *testing.T, raw string) types.JID {
	t.Helper()
	jid, err := types.ParseJID(raw)
	if err != nil {
		t.Fatalf("ParseJID(%q): %v", raw, err)
	}
	return jid
}
