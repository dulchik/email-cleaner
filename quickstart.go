package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Email struct {
	ID 			string
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

var (
	emails []Email
	senderGroups = make(map[string]*SenderGroup)
	mu sync.Mutex
)


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

	// Background goroutine to fethc emails incrementally
	go func() {
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
		
		mu.Lock()
		for _, m := range resp.Messages {
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
			email := Email{
				ID: 		 m.Id,
				From: 		 from,
				Subject: 	 subject,
				Unsubscribe: unsubscribe,
				Labels: 	 msg.LabelIds,
			}
			emails = append(emails, email)

			// Incremental batching
			if _, ok := senderGroups[from]; !ok {
				senderGroups[from] = &SenderGroup{
					Sender: from,
					Emails: []Email{},
				}
			}
			senderGroups[from].Emails = append(senderGroups[from].Emails, email)
			senderGroups[from].Count = len(senderGroups[from].Emails)
		}
		mu.Unlock()

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	log.Printf("Finished fetching all emails. Total: %d\n", len(emails))
	}()

	// HTTP Handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if len(emails) == 0 {
			fmt.Fprintln(w, "Loading emails... please refresh in a moment") 
			return
		}

		// Parse query params for sorting and filtering
		sortBy := r.URL.Query().Get("sort_by")
		order := r.URL.Query().Get("order")
		filterUnsub := r.URL.Query().Get("filter_unsub") == "on"
	
		// Convert map to slice for sorting
		var sortedGroups []SenderGroup
		for _, g := range senderGroups {
			sortedGroups = append(sortedGroups, *g)
		}
		// Sorting
		switch sortBy {
		case "sender":
			sort.Slice(sortedGroups, func(i, j int) bool {
				if order == "asc" {
					return sortedGroups[i].Sender < sortedGroups[j].Sender
				}
				return sortedGroups[i].Sender > sortedGroups[j].Sender
			})
		case "count":
			sort.Slice(sortedGroups, func(i, j int) bool {
				if order == "asc" {
					return sortedGroups[i].Count < sortedGroups[j].Count
				}
				return sortedGroups[i].Count > sortedGroups[j].Count
			})
		}

		// HTML output
		fmt.Fprintln(w, `<html><body>`)
		fmt.Fprintln(w, `<h1>Inbox Cleaner Dashboard</h1>`)

		// Sorting/filter form
		fmt.Fprintln(w, `<form method="get">
			Sort by:
			<select name="sort_by">
				<option value="count">Email count</option>
				<option value="sender">Sender</option>
			</select>
			Order:
			<select name="order">
				<option value="desc">Descending</option>
				<option value="asc">Ascending</option>
			</select>
			<label><input type="checkbox" name="filter_unsub"> Only with Unsubscribe</label>
			<input type="submit" value="Apply">
		</form>`)

		// JS for toggle
		fmt.Fprintln(w, `<script>
		function toggleTable(id) {
			var x = document.getElementById(id);
			if (x.style.display === "none") { x.style.display = "table"; }
			else { x.style.display = "none"; }
		}
		function bulkDelete(ids) {
			if(!confirm('Delete all emails for this sender?')) return;
			fetch('/delete', {
				method: 'POST',
				headers: {'Content-Type': 'application/json'},
				body: JSON.stringify({ids: ids})
			}).then(()=>{ location.reload(); });
		}
		</script>`)
		
		for i, g := range sortedGroups {
			tableID := fmt.Sprintf("table-%d", i) // unique ID per sender
			fmt.Fprintf(w, `<h2 onclick="toggleTable('%s')" style="cursor:pointer;">%s (%d emails) &#9660;</h2>`,
				tableID, g.Sender, g.Count)

			// Bulk delete button
			var msgIDs []string
			for _, e := range g.Emails {
				msgIDs = append(msgIDs, e.ID)
			}
			fmt.Fprintf(w, `<button onclick='bulkDelete(%q)'>Delete All</button>`, msgIDs)

			fmt.Fprintf(w, `<table border='1' id='%s' style='display:none;'>
			<tr><th>Subject</th><th>Unsubscribe</th><th>Labels</th></tr>`, tableID)

			for _, e := range g.Emails {
				if filterUnsub && e.Unsubscribe == "" {
					continue
				}
				unsubBtn := "N/A"
				if e.Unsubscribe != "" {
					if strings.HasPrefix(e.Unsubscribe, "mailto:") {
						unsubBtn = fmt.Sprintf(`<form style="display:inline;" method="post" action="/unsubscribe">
							<input type="hidden" name="id" value="%s">
							<input type="submit" value="Unsubscribe">
						</form>`, e.ID)
					} else {
						unsubBtn = fmt.Sprintf(`<a href="%s" target="_blank"><button>Unsubscribe</button></a>`, e.Unsubscribe)
					}
				}				
				fmt.Fprintf(w, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>",
					e.Subject, unsubBtn, strings.Join(e.Labels, ", "))
			}
			fmt.Fprintln(w, "</table>")
		}
		fmt.Fprintln(w, "</body></html>")
	})

	// Handle mailto unsubscribe
	http.HandleFunc("/unsubscribe", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id := r.FormValue("id")
		var target Email
		mu.Lock()
		for _, e := range emails {
			if e.ID == id {
				target = e
				break
			}
		}
		mu.Unlock()

		if target.ID == "" {
			fmt.Fprintln(w, "Email not found")
			return
		}

		parts := strings.SplitN(target.Unsubscribe[7:], "?", 2)
		address := parts[0]
		message := fmt.Sprintf("To: %s\r\nSubject: Unsubscribe\r\n\r\nPlease unsubscribe me.\r\n", address)
		_, err := srv.Users.Messages.Send("me", &gmail.Message{
			Raw: base64.URLEncoding.EncodeToString([]byte(message)),
		}).Do()
		if err != nil {
			fmt.Fprintf(w, "Failed to unsubscribe: %v", err)
			return
		}
		fmt.Fprintln(w, "Unsubscribed successfully! <a href='/'>Back</a>")
	})

	// Bulk delete endpoint
	http.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		type Req struct {
			Ids []string `json:"ids"`
		}
		var req Req
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Ids) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		srv.Users.Messages.BatchDelete(user, &gmail.BatchDeleteMessagesRequest{
			Ids: req.Ids,
		}).Do()
		w.WriteHeader(http.StatusOK)
	})


	log.Println("Server running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))

}
