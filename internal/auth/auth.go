package auth

import (
  "context"
  "encoding/json"
  "fmt"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "path/filepath"

  "golang.org/x/oauth2"
  "golang.org/x/oauth2/google"
  "google.golang.org/api/gmail/v1"
  "google.golang.org/api/option"
)

const tokenFile = "token.json" // store securely in production

// Read credentials.json downloaded from Google Cloud Console
func getConfig(credentialsPath string, scopes ...string) (*oauth2.Config, error) {
  b, err := ioutil.ReadFile(credentialsPath)
  if err != nil {
    return nil, err
  }
  config, err := google.ConfigFromJSON(b, scopes...)
  if err != nil {
    return nil, err
  }
  return config, nil
}

func tokenFromFile(path string) (*oauth2.Token, error) {
  f, err := os.Open(path)
  if err != nil {
    return nil, err
  }
  defer f.Close()
  tok := &oauth2.Token{}
  err = json.NewDecoder(f).Decode(tok)
  return tok, err
}

func saveToken(path string, token *oauth2.Token) error {
  fmt.Printf("Saving token to %s\n", path)
  f, err := os.Create(path)
  if err != nil { return err }
  defer f.Close()
  return json.NewEncoder(f).Encode(token)
}

func getClient(ctx context.Context, config *oauth2.Config) (*http.Client, error) {
  // Try existing token
  tok, err := tokenFromFile(tokenFile)
  if err != nil {
    // No token => start Auth flow (loopback)
    authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
    fmt.Printf("Go to the following link in your browser then type the code:\n%v\n", authURL)

    // Alternatively: open browser and start local server to capture redirect.
    fmt.Print("Enter authorization code: ")
    var code string
    if _, err := fmt.Scan(&code); err != nil {
      return nil, err
    }
    tok, err = config.Exchange(ctx, code)
    if err != nil { return nil, err }
    if err := saveToken(tokenFile, tok); err != nil { return nil, err }
  }
  return config.Client(ctx, tok), nil
}

func NewGmailService(credentialsPath string, scopes ...string) (*gmail.Service, error) {
  ctx := context.Background()
  config, err := getConfig(credentialsPath, scopes...)
  if err != nil { return nil, err }
  client, err := getClient(ctx, config)
  if err != nil { return nil, err }
  srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
  if err != nil { return nil, err }
  return srv, nil
}

