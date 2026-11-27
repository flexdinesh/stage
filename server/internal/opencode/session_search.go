package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.SessionSearcher = (*Adapter)(nil)

const sessionSearchUnavailable = "Search is unavailable on OpenCode. Enter a session link or ID."

type sessionListContract struct {
	available, order, archived, root bool
}

// Inspect the optional roster contract independently of the operations
// required to connect to OpenCode.
func inspectSessionList(op operation, components map[string]json.RawMessage) sessionListContract {
	if op.OperationID != "session.list" {
		return sessionListContract{}
	}
	var parameters []struct {
		Name, In string
		Schema   json.RawMessage
	}
	var responses map[string]struct {
		Content map[string]struct{ Schema json.RawMessage }
	}
	if json.Unmarshal(op.Parameters, &parameters) != nil || json.Unmarshal(op.Responses, &responses) != nil {
		return sessionListContract{}
	}

	schemas := listSchemas(components)
	query := make(map[string]json.RawMessage, len(parameters))
	for _, p := range parameters {
		if p.In == "query" {
			query[p.Name] = p.Schema
		}
	}
	page := schemas.decode(responses["200"].Content["application/json"].Schema)
	cursor := schemas.decode(page.Properties["cursor"])
	return sessionListContract{
		available: schemas.allows(query["limit"], "", "string", "integer", "number") &&
			schemas.allows(query["cursor"], "", "string") &&
			page.Type == "object" && schemas.decode(page.Properties["data"]).Type == "array" &&
			cursor.Type == "object" && schemas.allows(cursor.Properties["next"], "", "string"),
		order:    schemas.allows(query["order"], "desc", "string"),
		archived: schemas.allows(query["archived"], "false", "string", "boolean"),
		root: schemas.any(query["parentID"], func(shape listSchema) bool {
			return shape.Type == "string" && slices.Contains(shape.Enum, "null")
		}),
	}
}

type listSchema struct {
	schemaShape
	Type  string
	AnyOf []json.RawMessage
	Enum  []string
}

type listSchemas map[string]json.RawMessage

func (s listSchemas) decode(raw json.RawMessage) listSchema {
	var shape listSchema
	if json.Unmarshal(raw, &shape) != nil {
		return listSchema{}
	}
	if ref, ok := strings.CutPrefix(shape.Ref, "#/components/schemas/"); ok {
		shape = listSchema{}
		if json.Unmarshal(s[ref], &shape) != nil {
			return listSchema{}
		}
	}
	return shape
}

func (s listSchemas) allows(raw json.RawMessage, value string, types ...string) bool {
	return s.any(raw, func(shape listSchema) bool {
		return slices.Contains(types, shape.Type) &&
			(value == "" || len(shape.Enum) == 0 || slices.Contains(shape.Enum, value))
	})
}

func (s listSchemas) any(raw json.RawMessage, matches func(listSchema) bool) bool {
	shape := s.decode(raw)
	return matches(shape) || slices.ContainsFunc(shape.AnyOf, func(variant json.RawMessage) bool {
		return matches(s.decode(variant))
	})
}

type listedSession struct {
	ID, Title, ParentID string
	Location            location
	Time                wireTime
}

// SearchSessions reads one bounded native roster page. Local matching leaves
// its order and continuation intact, including pages with zero matches.
func (a *Adapter) SearchSessions(ctx context.Context, q execution.SessionSearchQuery) (execution.SessionSearchPage, error) {
	var out execution.SessionSearchPage
	if err := q.Valid(); err != nil {
		return out, err
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return out, err
	}
	if !contract.search.available {
		return out, &execution.SessionSearchUnsupported{Reason: sessionSearchUnavailable}
	}

	params := url.Values{"limit": {strconv.Itoa(q.Limit)}}
	if q.Cursor != "" {
		params.Set("cursor", q.Cursor)
	} else if contract.search.order {
		params.Set("order", "desc")
	}
	if contract.search.archived {
		params.Set("archived", "false")
	}
	if contract.search.root {
		params.Set("parentID", "null")
	}
	var page struct {
		Data   []listedSession
		Cursor *struct{ Next string }
	}
	if err := a.get(ctx, "/api/session?"+params.Encode(), &page); err != nil {
		return out, err
	}
	if page.Data == nil || len(page.Data) > q.Limit || page.Cursor == nil ||
		len(page.Cursor.Next) > execution.SessionSearchMaxCursor || (q.Cursor != "" && page.Cursor.Next == q.Cursor) {
		return out, errors.New("invalid OpenCode session list page")
	}

	query := strings.ToLower(strings.TrimSpace(q.Query))
	out.Sessions = make([]execution.ListedSession, 0, len(page.Data))
	out.NextCursor = page.Cursor.Next
	for _, s := range page.Data {
		if !sessionID.MatchString(s.ID) {
			return execution.SessionSearchPage{}, errors.New("invalid OpenCode roster session ID")
		}
		if query != "" && !strings.Contains(strings.ToLower(s.Title), query) &&
			!strings.Contains(strings.ToLower(s.Location.Directory), query) &&
			!strings.Contains(strings.ToLower(s.ID), query) {
			continue
		}
		item := execution.ListedSession{
			Session:     execution.SessionRef{ConnectionID: a.connection, ExternalSessionID: s.ID},
			Title:       s.Title,
			Workspace:   s.Location.Directory,
			StartedAt:   millis(s.Time.Created),
			Eligibility: available,
		}
		switch {
		case s.Time.Archived > 0 || s.ParentID != "":
			item.Eligibility = execution.Availability{Reason: "Choose a top-level, unarchived session."}
		case s.Location.Directory == "":
			item.Eligibility = execution.Availability{Reason: "OpenCode did not list a workspace for this session. Enter its ID to check it directly."}
		}
		out.Sessions = append(out.Sessions, item)
	}
	return out, nil
}
