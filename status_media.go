package main

import "go.mau.fi/whatsmeow/types"

// isStatusChat reports whether the chat is the Status feed (status@broadcast), where every
// contact's Status post arrives as an ordinary Message event.
//
// The Status feed of an account with many contacts is a steady stream of photos and videos
// that nobody asked this API to keep. Downloading each one (and inlining it as base64 in the
// webhook) costs bandwidth and memory for payloads the integration discards, so the media
// download is skipped for these chats. The event itself, with its metadata and thumbnail,
// is still delivered.
func isStatusChat(chat types.JID) bool {
	return chat.Server == types.BroadcastServer && chat.User == "status"
}
