// Package api has type definitions for the remoteStorage protocol
// (draft-dejong-remotestorage).
package api

import (
	"fmt"
	"strings"
)

// FolderDescriptionContext is the @context of a folder listing (-02 and later)
const FolderDescriptionContext = "http://remotestorage.io/spec/folder-description"

// Listing is the JSON-LD body returned by a GET on a folder (path ending in /).
//
// Keys of Items are item names (not URI encoded). Folders end in "/" and
// carry only an ETag; documents also carry Content-Type, Content-Length and,
// from draft -11 on, Last-Modified.
type Listing struct {
	Context string          `json:"@context"`
	Items   map[string]Item `json:"items"`
}

// Item is a folder or document description inside a Listing
type Item struct {
	ETag          string `json:"ETag"`
	ContentType   string `json:"Content-Type"`
	ContentLength *int64 `json:"Content-Length"`
	LastModified  string `json:"Last-Modified"`
}

// Error is returned for non-2xx responses
type Error struct {
	StatusCode int
	Status     string
	Method     string
	Path       string
	Message    string
}

// Error satisfies the error interface
func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Message)
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	if msg == "" {
		return fmt.Sprintf("%s %s: %s", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("%s %s: %s: %s", e.Method, e.Path, e.Status, msg)
}
