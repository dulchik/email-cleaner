package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type Email struct {
	From        string   `json:"from"`
	Subject     string   `json:"subject"`
	Unsubscribe string   `json:"unsubscribe"`
	Labels      []string `json:"labels"`
}

var (
	emails        []Email
	fetchedEmails int
	mu            sync.Mutex
	allMessages   []*gmail.Message
)

func getClient(config *oauth2.Config) *http.Client {
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	codeCh := make(chan string)
	srv := &http.Server{Addr: ":8081"}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		fmt.Fprintf(w, "Authorization successful! You can close this window.")
		codeCh <- code
		go srv.Shutdown(context.Background())
	})
	go func() {
		log.Println("OAuth server running on http://localhost:8081/")
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Auth server error: %v", err)
		}
	}()
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser:\n%v\n", authURL)
	code := <-codeCh
	tok, err := config.Exchange(context.Background(), code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

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

func saveToken(path string, token *oauth2.Token) {
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
	config, err := google.ConfigFromJSON(b, gmail.GmailModifyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	config.RedirectURL = "http://localhost:8081/"

	client := getClient(config)
	srvGmail, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to retrieve Gmail client: %v", err)
	}
	user := "me"

	// Fetch message IDs
	pageToken := ""
	for {
		req := srvGmail.Users.Messages.List(user).LabelIds("INBOX").MaxResults(500)
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
	log.Printf("Total messages to fetch: %d", len(allMessages))

	emailCh := make(chan Email, 100)
	var wg sync.WaitGroup
	workers := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range emailCh {
				mu.Lock()
				emails = append(emails, e)
				fetchedEmails++
				mu.Unlock()
			}
		}()
	}

	go func() {
		sem := make(chan struct{}, workers)
		for _, msg := range allMessages {
			sem <- struct{}{}
			go func(mID string) {
				defer func() { <-sem }()
				msgDetail, err := srvGmail.Users.Messages.Get(user, mID).Format("metadata").Do()
				if err != nil {
					log.Printf("Failed to fetch %s: %v", mID, err)
					return
				}
				var from, subject, unsubscribe string
				for _, h := range msgDetail.Payload.Headers {
					switch h.Name {
					case "From":
						from = h.Value
					case "Subject":
						subject = h.Value
					case "List-Unsubscribe":
						unsubscribe = h.Value
					}
				}
				emailCh <- Email{From: from, Subject: subject, Unsubscribe: unsubscribe, Labels: msgDetail.LabelIds}
			}(msg.Id)
		}
		for i := 0; i < workers; i++ {
			sem <- struct{}{}
		}
		close(emailCh)
	}()

	// HTTP endpoints
	http.HandleFunc("/emails", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if emails == nil {
			emails = []Email{}
		}
		// Filtering and sorting
		filtered := emails
		filter := r.URL.Query().Get("filter")
		sortBy := r.URL.Query().Get("sort")
		if filter == "withUnsub" {
			tmp := []Email{}
			for _, e := range filtered {
				if e.Unsubscribe != "" {
					tmp = append(tmp, e)
				}
			}
			filtered = tmp
		}
		if sortBy == "fromAsc" {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].From < filtered[j].From })
		} else if sortBy == "fromDesc" {
			sort.Slice(filtered, func(i, j int) bool { return filtered[i].From > filtered[j].From })
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"emails":       filtered,
			"fetchedEmails": fetchedEmails,
			"totalEmails":   len(allMessages),
		})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, `<html><body><h1>Inbox Cleaner Dashboard</h1>`)
		fmt.Fprintln(w, `<div>Sort: <select id='sort'><option value='fromAsc'>From Asc</option><option value='fromDesc'>From Desc</option></select> Filter: <select id='filter'><option value='all'>All</option><option value='withUnsub'>With Unsubscribe</option></select></div>`)
		fmt.Fprintln(w, `<div id='progress'>Loading...</div>`)
		fmt.Fprintln(w, `<div id='emails'></div>`)
		fmt.Fprintln(w, `<script>
function fetchEmails(){
  let sort = document.getElementById('sort').value;
  let filter = document.getElementById('filter').value;
  fetch('/emails?sort='+sort+'&filter='+filter)
  .then(resp => resp.json())
  .then(data => {
    document.getElementById('progress').innerText = 'Fetched ' + data.fetchedEmails + ' of ' + data.totalEmails;
    let container = document.getElementById('emails');
    container.innerHTML = '';
    if(data.emails && Array.isArray(data.emails)){
      data.emails.forEach(e=>{
        let div = document.createElement('div');
        let unsub = e.unsubscribe ? '<a href="'+e.unsubscribe+'" target="_blank">Unsubscribe</a>' : 'N/A';
        div.innerHTML = e.from + ' | ' + e.subject + ' | ' + unsub;
        container.appendChild(div);
      });
    }
  })
  .catch(err=>console.log(err));
}
setInterval(fetchEmails, 1000);
</script>`)
		fmt.Fprintln(w, `</body></html>`)
	})

	log.Println("Dashboard running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
	wg.Wait()
}
