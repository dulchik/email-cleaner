package parser

import (
  "bytes"
  "net/mail"
  "regexp"
  "strings"

  "github.com/PuerkitoBio/goquery"
)

var unsubscribeRegex = regexp.MustCompile(`(?i)unsubscribe`)

// ParseRawRFC822 extracts headers, from, subject, and candidate unsubscribe links
func ParseRawRFC822(raw string) (from, subject string, listUnsub []string, bodyUnsub []string, err error) {
  msg, err := mail.ReadMessage(strings.NewReader(raw))
  if err != nil { return }
  from = msg.Header.Get("From")
  subject = msg.Header.Get("Subject")

  // Check List-Unsubscribe header (may contain: <mailto:...>, <https://...>)
  lu := msg.Header.Get("List-Unsubscribe")
  if lu != "" {
    // split by comma/angle brackets
    // Example: "<mailto:unsubscribe@domain.com>, <https://domain.com/unsub?x=1>"
    parts := strings.Split(lu, ",")
    for _, p := range parts {
      p = strings.TrimSpace(p)
      p = strings.Trim(p, "<>")
      listUnsub = append(listUnsub, p)
    }
  }

  // Read body to search fallback links
  buf := new(bytes.Buffer)
  _, _ = buf.ReadFrom(msg.Body)
  body := buf.String()
  // Quick heuristic: search for 'unsubscribe' in body and parse html anchors
  if unsubscribeRegex.MatchString(body) {
    doc, err2 := goquery.NewDocumentFromReader(strings.NewReader(body))
    if err2 == nil {
      doc.Find("a").Each(func(i int, s *goquery.Selection) {
        text := strings.TrimSpace(s.Text())
        href, _ := s.Attr("href")
        if href != "" && (unsubscribeRegex.MatchString(text) || strings.Contains(strings.ToLower(href), "unsubscribe")) {
          bodyUnsub = append(bodyUnsub, href)
        }
      })
    }
    // Also find mailto links by regex
    re := regexp.MustCompile(`(?i)mailto:[\w\-\.\+%]+@[\w\.\-]+\.[a-zA-Z]{2,}`)
    for _, m := range re.FindAllString(body, -1) { bodyUnsub = append(bodyUnsub, m) }
  }
  return
}

