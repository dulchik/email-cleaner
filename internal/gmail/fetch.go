package gmailwrapper

import (
  "context"
  "encoding/base64"
  "fmt"
  "google.golang.org/api/gmail/v1"
  "strings"
)

type Fetcher struct {
  srv *gmail.Service
}

func NewFetcher(srv *gmail.Service) *Fetcher { return &Fetcher{srv: srv} }

// List message IDs with optional query (Gmail search syntax)
func (f *Fetcher) ListMessageIDs(maxResults int64, query string) ([]string, error) {
  call := f.srv.Users.Messages.List("me").MaxResults(maxResults)
  if query != "" { call = call.Q(query) }
  resp, err := call.Do()
  if err != nil { return nil, err }
  ids := make([]string, 0, len(resp.Messages))
  for _, m := range resp.Messages { ids = append(ids, m.Id) }
  return ids, nil
}

// Get raw message (RFC 822 base64url)
func (f *Fetcher) GetRawMessage(msgID string) (string, error) {
  m, err := f.srv.Users.Messages.Get("me", msgID).Format("raw").Do()
  if err != nil { return "", err }
  raw := m.Raw
  // decode base64url:
  decoded, err := base64.RawURLEncoding.DecodeString(raw)
  if err != nil {
    // Gmail may give standard base64, try that
    b, err2 := base64.StdEncoding.DecodeString(raw)
    if err2 != nil {
      return "", fmt.Errorf("decode error: %v / %v", err, err2)
    }
    decoded = b
  }
  return string(decoded), nil
}

// Or fetch full with payload (if you prefer part-by-part parsing)
func (f *Fetcher) GetFullMessage(msgID string) (*gmail.Message, error) {
  return f.srv.Users.Messages.Get("me", msgID).Format("full").Do()
}

