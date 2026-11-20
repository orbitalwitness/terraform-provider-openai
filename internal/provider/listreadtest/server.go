// Package listreadtest provides an in-memory collection API for offline tests
// of resources that refresh from one cached list request per parent scope.
package listreadtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Server serves one collection: GET and POST on the list path, and GET, POST
// and DELETE on list path + "/{id}". Lists are paged by pageSize items with an
// "after" cursor, so reads must follow pagination. It records how reads
// reached it.
type Server struct {
	URL string

	mu         sync.Mutex
	listPath   string
	newItem    func(id string, body map[string]any) map[string]any
	items      []map[string]any
	nextID     int
	listCalls  int
	listPages  int
	itemGets   int
	unexpected []string
}

// New starts a server for the collection at listPath (without the "/v1"
// prefix). newItem builds the stored object from a create request body.
func New(t *testing.T, listPath string, newItem func(id string, body map[string]any) map[string]any) *Server {
	t.Helper()
	s := &Server{listPath: "/v1" + listPath, newItem: newItem}
	server := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(server.Close)
	s.URL = server.URL + "/v1"
	return s
}

func (s *Server) handle(w http.ResponseWriter, req *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	if req.URL.Path == s.listPath {
		switch req.Method {
		case http.MethodGet:
			s.list(w, req)
		case http.MethodPost:
			body, ok := s.decode(w, req)
			if !ok {
				return
			}
			s.nextID++
			item := s.newItem(fmt.Sprintf("item_%d", s.nextID), body)
			s.items = append(s.items, item)
			s.write(w, item)
		default:
			s.reject(w, req)
		}
		return
	}

	id, found := strings.CutPrefix(req.URL.Path, s.listPath+"/")
	if !found || id == "" || strings.Contains(id, "/") {
		s.reject(w, req)
		return
	}
	index := s.index(id)
	if req.Method == http.MethodGet {
		s.itemGets++
	}
	if index < 0 {
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
		return
	}
	switch req.Method {
	case http.MethodGet:
		s.write(w, s.items[index])
	case http.MethodPost:
		body, ok := s.decode(w, req)
		if !ok {
			return
		}
		maps.Copy(s.items[index], body)
		s.write(w, s.items[index])
	case http.MethodDelete:
		s.items = append(s.items[:index], s.items[index+1:]...)
		s.write(w, map[string]any{"id": id, "deleted": true})
	default:
		s.reject(w, req)
	}
}

const pageSize = 2

func (s *Server) list(w http.ResponseWriter, req *http.Request) {
	start := 0
	if after := req.URL.Query().Get("after"); after != "" {
		index := s.index(after)
		if index < 0 {
			http.Error(w, "unknown cursor", http.StatusBadRequest)
			return
		}
		start = index + 1
	} else {
		s.listCalls++
	}
	s.listPages++
	end := min(start+pageSize, len(s.items))
	page := s.items[start:end]
	response := map[string]any{"object": "list", "data": page, "has_more": end < len(s.items)}
	if len(page) > 0 {
		response["first_id"], response["last_id"] = page[0]["id"], page[len(page)-1]["id"]
	}
	s.write(w, response)
}

func (s *Server) index(id string) int {
	for i, item := range s.items {
		if item["id"] == id {
			return i
		}
	}
	return -1
}

func (s *Server) decode(w http.ResponseWriter, req *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func (s *Server) write(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) reject(w http.ResponseWriter, req *http.Request) {
	s.unexpected = append(s.unexpected, req.Method+" "+req.URL.Path)
	http.Error(w, "unexpected request", http.StatusNotFound)
}

// DeleteFirst removes the first stored item, as if it were deleted outside Terraform.
func (s *Server) DeleteFirst() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = s.items[1:]
}

// ResetCounts clears the read counters.
func (s *Server) ResetCounts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls, s.listPages, s.itemGets = 0, 0, 0
}

// CheckReads fails the test if any read used a single-item GET, if more than
// maxListCalls list scans were made, if no scan followed a second page, or if
// an unexpected request arrived.
func (s *Server) CheckReads(maxListCalls int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.unexpected) > 0 {
		return fmt.Errorf("unexpected requests: %v", s.unexpected)
	}
	if s.itemGets > 0 {
		return fmt.Errorf("expected reads from the list, got %d single-item GETs", s.itemGets)
	}
	if s.listCalls == 0 || s.listCalls > maxListCalls {
		return fmt.Errorf("expected 1-%d list scans, got %d", maxListCalls, s.listCalls)
	}
	if s.listPages <= s.listCalls {
		return fmt.Errorf("expected list scans to follow pagination, got %d pages for %d scans", s.listPages, s.listCalls)
	}
	return nil
}
