package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sort"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Email struct {
	From 		string
	Subject 	string
	Unsubscribe string
	Labels		[]string
}

type SenderGroup struct {
	Sender string
	Emails []Email
	Count  int
}


// Retrieve a token, saves the token, then returns the generated client.
func getClient(config *oauth2.Config) *http.Client {
	// The file token.json stores the user's access and refresh tokens, and is
	// created automatically when the authorization flow completes for the first
	// time.
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	// start temporary server
	codeCh := make(chan string)
	srv := &http.Server{Addr: ":8080"}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		fmt.Fprintf(w, "Authorization successful! You can close this window.")
		codeCh <- code
		go srv.Shutdown(context.Background())
	})

	go func() {
		log.Println("Starting local server on http://localhost:8080/")
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Auth server error: %v", err)
		}
	}()

	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	code := <-codeCh
	tok, err := config.Exchange(context.Background(), code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func main() {
		ctx := context.Background()
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, gmail.GmailModifyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	config.RedirectURL = "http://localhost:8080/"

	client := getClient(config)

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Gmail client: %v", err)
	}

	user := "me"
	var emails []Email
	var mu sync.Mutex
	senderGroups := make(map[string]*SenderGroup)


	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if len(emails) == 0 {
			fmt.Fprintln(w, "Loading emails... please refresh in a moment") 
			return
		}
	
		// Convert map to slice and sort
		var sortedGroups []SenderGroup
		for _, g := range senderGroups {
			sortedGroups = append(sortedGroups, *g)
		}

		// Sort descending by count
		sort.Slice(sortedGroups, func(i, j int) bool {
			return sortedGroups[i].Count > sortedGroups[j].Count
		})

		fmt.Fprintln(w, "<html><body><h1>Inbox Emails by Sender</h1>")

		// Include simple JS for toggling
		fmt.Fprintln(w, `<script>
		function toggleTable(id) {
  		var x = document.getElementById(id);
  		if (x.style.display === "none") {
    		x.style.display = "table";
  		} else {
    		x.style.display = "none";
  		}
		}
		</script>`)

		for i, g := range sortedGroups {
			tableID := fmt.Sprintf("table-%d", i) // unique ID per sender
			fmt.Fprintf(w, `<h2 onclick="toggleTable('%s')" style="cursor:pointer;">%s (%d emails) &#9660;</h2>`,
				tableID, g.Sender, g.Count)

			fmt.Fprintf(w, `<table border='1' id='%s' style='display:none;'>
			<tr><th>Subject</th><th>Unsubscribe</th><th>Labels</th></tr>`, tableID)

			for _, e := range g.Emails {
				unsubBtn := "N/A"
				if e.Unsubscribe != "" {
					unsubBtn = fmt.Sprintf("<a href='%s' target='_blank'><button>Unsubscribe</button></a>", e.Unsubscribe)
				}
				fmt.Fprintf(w, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>",
					e.Subject, unsubBtn, strings.Join(e.Labels, ", "))
			}

			fmt.Fprintln(w, "</table>")
		}

		fmt.Fprintln(w, "</body></html>")
	})

	go func() {
	var allMessages []*gmail.Message
	pageToken := ""
	for {
		req := srv.Users.Messages.List(user).LabelIds("INBOX").MaxResults(500)
		if pageToken != "" {
			req.PageToken(pageToken)
		}
		resp, err := req.Do()
		if err != nil {
			log.Fatalf("Unable to retrieve message: %v", err)
		}
		allMessages = append(allMessages, resp.Messages...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	fmt.Printf("Total emails in INBOX: %d\n", len(allMessages))

	for _, m := range allMessages {
		msg, err := srv.Users.Messages.Get(user, m.Id).Format("metadata").Do()
		if err != nil {
			log.Printf("Unable to get message %s: %v", m.Id, err)
			continue
		}

		var from, subject, unsubscribe string
		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "From":
				from = h.Value
			case "Subject":
				subject = h.Value
			case "List-Unsubscribe":
				unsubscribe = h.Value
			}
		}

		mu.Lock()
		emails = append(emails, Email{
			From: from,
			Subject: subject,
			Unsubscribe: unsubscribe,
			Labels: msg.LabelIds,
		})

		// Incremental batching
		if _, ok := senderGroups[from]; !ok {
			senderGroups[from] = &SenderGroup{
				Sender: from,
				Emails: []Email{},
			}
		}

		senderGroups[from].Emails = append(senderGroups[from].Emails, Email{
			From: from,
			Subject: subject,
			Unsubscribe: unsubscribe,
			Labels: msg.LabelIds,
		})
		senderGroups[from].Count = len(senderGroups[from].Emails)
		mu.Unlock()
	}
		// Batch emails by sender
		mu.Lock()
		senderMap := make(map[string]*SenderGroup)
		for _, e := range emails {
						senderMap[e.From].Emails = append(senderMap[e.From].Emails, e)
		}
		mu.Unlock()
	}()


	log.Println("Server running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))

}
