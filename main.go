package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

type Server struct {
	kkClient *KarakeepClient
	wfClient *WorkflowyClient
	errors   []string
}

type WorkflowyClient struct {
	URL      string
	APIKey   string
	Disabled bool
}

type CreateNodeRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Position string `json:"position"`
}

type KarakeepClient struct {
	URL      string
	APIKey   string
	Disabled bool
}

type KarakeepBookmark struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Content BookmarkContent `json:"content"`
}

type BookmarkContent struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (b KarakeepBookmark) String() string {
	return fmt.Sprintf("{id=%s} {title=%s} {content.type=%s} {content.title=%s} {content.url=%s}", b.ID, b.Title, b.Content.Type, b.Content.Title, b.Content.URL)
}

func (wf *WorkflowyClient) CreateNode(req CreateNodeRequest) error {
	if wf.Disabled {
		return nil
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/nodes", wf.URL), bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+wf.APIKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func getEnvOrFatal(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s variable is not set", key)
	}

	return value
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func (kc *KarakeepClient) getBookmark(id string) (*KarakeepBookmark, error) {
	if kc.Disabled {
		// return dummy response for testing
		return &KarakeepBookmark{ID: "dummyid", Title: "dummy", Content: BookmarkContent{Type: "link"}}, nil
	}

	httpReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/bookmarks/%s", kc.URL, id), nil)
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+kc.APIKey)

	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	var bookmark KarakeepBookmark
	if err := json.Unmarshal(respBody, &bookmark); err != nil {
		return nil, fmt.Errorf("err decoding response : %v", err)
	}

	return &bookmark, nil
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	s.errors = append(s.errors, err.Error())

	log.Printf("err: %v", err)
	//w.WriteHeader(http.StatusInternalServerError)
	//fmt.Fprintf(w, "{\"error\": \"%v\"}", err)
}

func getFileName(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return "missing_filename"
	}

	name, err := url.PathUnescape(path.Base(parsed.Path))
	if err != nil {
		return "missing_filename"
	}

	return name
}

func (s *Server) karakeepHandler(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	var webhookReq struct {
		Type       string `json:"type"`
		BookmarkID string `json:"bookmarkId"`
		Operation  string `json:"operation"`
		URL        string `json:"url"`
	}

	data, err := io.ReadAll(req.Body)
	if err != nil {
		s.writeError(w, fmt.Errorf("error reading request body: %v", err))
		return
	}
	log.Printf("webhook request: %v", string(data))

	if err := json.Unmarshal(data, &webhookReq); err != nil {
		s.writeError(w, fmt.Errorf("error decoding webhook request: %v", err))
		return
	}

	var entry string
	if webhookReq.Operation == "created" {
		if strings.Contains(webhookReq.URL, "pdf") {
			entry = fmt.Sprintf("%s - [Karakeep](karakeep://dashboard/bookmarks/%s)", getFileName(webhookReq.URL), webhookReq.BookmarkID)
		} else if webhookReq.Type == "asset" {
			entry = fmt.Sprintf("untitled.pdf - [Karakeep](karakeep://dashboard/bookmarks/%s)", webhookReq.BookmarkID)
		} else {
			log.Printf("skipping request")
			return
		}
	} else if webhookReq.Operation == "crawled" {
		bookmark, err := s.kkClient.getBookmark(webhookReq.BookmarkID)
		if err != nil {
			s.writeError(w, fmt.Errorf("[https://bookmarks.mschoudhary.site/dashboard/preview/%s] error getting bookmark: %v", webhookReq.BookmarkID, err))
			return
		}

		log.Printf("bookmark: %v", bookmark)

		if bookmark.Title != "" {
			entry = bookmark.Title
		} else if bookmark.Content.Title != "" {
			entry = bookmark.Content.Title
		} else {
			entry = "Untitled"
		}

		entry = fmt.Sprintf("%s - [Link](%s) - [Karakeep](karakeep://dashboard/bookmarks/%s)", entry, webhookReq.URL, webhookReq.BookmarkID)
	} else {
		log.Printf("skipping request")
		return
	}

	createNodeReq := CreateNodeRequest{
		ParentID: "inboxkarakeep",
		Name:     entry,
		Position: "top",
	}

	if err := s.wfClient.CreateNode(createNodeReq); err != nil {
		s.writeError(w, fmt.Errorf("[https://bookmarks.mschoudhary.site/dashboard/preview/%s] error creating wf_node: %v", webhookReq.BookmarkID, err))
		return
	}

	log.Printf("wf node created, entry: %s", entry)
	json.NewEncoder(w).Encode(map[string]string{"status": "wf_node_created"})
}

func (s *Server) healthHandler(w http.ResponseWriter, req *http.Request) {
	if len(s.errors) != 0 {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string][]string{"errors": s.errors})
	} else {
		fmt.Fprintf(w, "{\"status\": \"ok\"}")
	}
}

func main() {
	testmode := flag.Bool("test", false, "Run in test mode")
	flag.Parse()

	server := &Server{
		kkClient: &KarakeepClient{
			URL: "https://bookmarks.mschoudhary.site",
		},
		wfClient: &WorkflowyClient{
			URL: "https://workflowy.com",
		},
	}

	if *testmode {
		server.kkClient.Disabled = true
		server.wfClient.Disabled = true
	} else {
		server.kkClient.APIKey = getEnvOrFatal("KARAKEEP_API_KEY")
		server.wfClient.APIKey = getEnvOrFatal("WORKFLOWY_API_KEY")
	}

	http.HandleFunc("/health", server.healthHandler)
	http.Handle("/karakeep", logMiddleware(http.HandlerFunc(server.karakeepHandler)))
	log.Print("listening on 8090 ...")
	log.Fatal(http.ListenAndServe(":8090", nil))
}
