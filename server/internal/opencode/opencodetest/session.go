package opencodetest

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Session is the wire snapshot of one test session. SetSession copies it.
type Session struct {
	Info                               map[string]any
	Permissions, Forms, Messages, Diff []map[string]any // messages are newest first
	Running                            bool
}

type Interaction struct {
	Path string
	Body map[string]any
}

func (s *Server) SetSession(id string, snapshot Session) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	var copy Session
	if err := json.Unmarshal(raw, &copy); err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = copy
}

func (s *Server) Interactions() []Interaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.interactions)
}

func (s *Server) serveSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/session/"), "/")
	write := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": data}) }
	if id == "active" && r.Method == http.MethodGet {
		active := map[string]any{}
		for id, session := range s.sessions {
			if session.Running {
				active[id] = map[string]string{"type": "running"}
			}
		}
		write(active)
		return
	}
	v, ok := s.sessions[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		switch rest {
		case "":
			write(v.Info)
		case "permission":
			write(nonNil(v.Permissions))
		case "form":
			write(nonNil(v.Forms))
		case "diff":
			write(nonNil(v.Diff))
		case "inbox":
			inbox := []map[string]any{}
			for _, call := range s.interactions {
				if call.Path == "/api/session/"+id+"/prompt" && call.Body["delivery"] != nil {
					inbox = append(inbox, admission(id, call.Body))
				}
			}
			write(inbox)
		case "message":
			messages := []map[string]any{}
			for _, m := range v.Messages {
				if kind := r.URL.Query().Get("type"); kind == "" || m["type"] == kind {
					messages = append(messages, m)
				}
			}
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			offset, _ := strconv.Atoi(q.Get("cursor"))
			offset = min(max(offset, 0), len(messages))
			end := len(messages)
			if limit > 0 {
				end = min(offset+limit, end)
			}
			cursor := map[string]string{}
			if end < len(messages) {
				cursor["next"] = strconv.Itoa(end)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": messages[offset:end], "cursor": cursor})
		default:
			if messageID, ok := strings.CutPrefix(rest, "message/"); ok {
				for _, m := range v.Messages {
					if m["id"] == messageID {
						write(m)
						return
					}
				}
			}
			http.NotFound(w, r)
		}
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	switch {
	case rest == "prompt":
		write(admission(id, body))
	case strings.HasSuffix(rest, "/reply"):
		kind, tail, _ := strings.Cut(rest, "/")
		requestID, _, _ := strings.Cut(tail, "/")
		requests := &v.Permissions
		if kind == "form" {
			requests = &v.Forms
		}
		i := slices.IndexFunc(*requests, func(req map[string]any) bool { return req["id"] == requestID })
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		*requests = slices.Delete(*requests, i, i+1)
		s.sessions[id] = v
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
		return
	}
	s.interactions = append(s.interactions, Interaction{Path: r.URL.Path, Body: body})
}

func nonNil(rows []map[string]any) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	return rows
}

func admission(session string, body map[string]any) map[string]any {
	return map[string]any{"id": body["id"], "sessionID": session, "type": "user", "delivery": body["delivery"],
		"payload": map[string]any{"text": body["text"], "metadata": body["metadata"]}}
}
