package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

type Server struct {
	kkClient *KarakeepClient
	wfClient *WorkflowyClient
	errors   []string
}

type WorkflowyClient struct {
	URL    string
	APIKey string
}

type CreateNodeRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Position string `json:"position"`
}

type KarakeepClient struct {
	URL    string
	APIKey string
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

func (s *Server) webhookHandler(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	var webhookReq struct {
		BookmarkID string `json:"bookmarkId"`
	}

	if err := json.NewDecoder(req.Body).Decode(&webhookReq); err != nil {
		s.writeError(w, fmt.Errorf("error decoding webhook request: %v", err))
		return
	}

	bookmark, err := s.kkClient.getBookmark(webhookReq.BookmarkID)
	if err != nil {
		s.writeError(w, fmt.Errorf("[https://bookmarks.mschoudhary.site/dashboard/preview/%s] error getting bookmark: %v", webhookReq.BookmarkID, err))
		return
	}

	log.Printf("bookmark: %v", bookmark)

	var entry string
	if bookmark.Title != "" {
		entry = bookmark.Title
	} else if bookmark.Content.Title != "" {
		entry = bookmark.Content.Title
	} else {
		entry = "Untitled"
	}

	if bookmark.Content.Type == "link" {
		entry = fmt.Sprintf("%s - [Link](%s)", entry, bookmark.Content.URL)
	}

	entry = fmt.Sprintf("%s - [Karakeep](https://bookmarks.mschoudhary.site/dashboard/preview/%s)", entry, bookmark.ID)

	createNodeReq := CreateNodeRequest{
		ParentID: "inboxkarakeep",
		Name:     entry,
		Position: "top",
	}

	if err := s.wfClient.CreateNode(createNodeReq); err != nil {
		s.writeError(w, fmt.Errorf("[https://bookmarks.mschoudhary.site/dashboard/preview/%s] error creating wf_node: %v", webhookReq.BookmarkID, err))
		return
	}

	log.Print("wf node created")
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
	server := &Server{
		kkClient: &KarakeepClient{
			URL:    "https://bookmarks.mschoudhary.site",
			APIKey: getEnvOrFatal("KARAKEEP_API_KEY"),
		},
		wfClient: &WorkflowyClient{
			URL:    "https://workflowy.com",
			APIKey: getEnvOrFatal("WORKFLOWY_API_KEY"),
		},
	}

	http.HandleFunc("/health", server.healthHandler)
	http.Handle("/webhook", logMiddleware(http.HandlerFunc(server.webhookHandler)))
	log.Print("listening on 8090 ...")
	log.Fatal(http.ListenAndServe(":8090", nil))
}
