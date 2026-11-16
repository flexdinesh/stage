package client

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/flexdinesh/stage/server/internal/db"
)

func journalPath(owner db.JournalOwner) (string, error) {
	if (owner.TaskID == nil) == (owner.TreeID == nil) {
		return "", errors.New("exactly one taskId or treeId is required")
	}
	if owner.TaskID != nil {
		if *owner.TaskID <= 0 {
			return "", errors.New("taskId must be positive")
		}
		return taskPath(*owner.TaskID, "journal"), nil
	}
	if *owner.TreeID <= 0 {
		return "", errors.New("treeId must be positive")
	}
	return treePath(*owner.TreeID, "journal"), nil
}

// ReadJournal reads previews; GetJournalEntry returns complete text.
func (c *Client) ReadJournal(ctx context.Context, owner db.JournalOwner, query db.JournalQuery) (db.JournalPage, error) {
	var out db.JournalPage
	path, err := journalPath(owner)
	if err != nil {
		return out, err
	}
	if owner.TaskID != nil && query.TaskID != nil {
		return out, errors.New("taskId filtering is only supported on a tree journal")
	}
	q := url.Values{}
	for key, value := range map[string]string{"kind": query.Kind, "category": query.Category, "q": query.Q, "cursor": query.Cursor} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if query.TaskID != nil {
		q.Set("taskId", strconv.FormatInt(*query.TaskID, 10))
	}
	if query.Limit != 0 {
		q.Set("limit", strconv.Itoa(query.Limit))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	err = c.do(ctx, "GET", path, nil, &out)
	return out, err
}

func (c *Client) GetJournalEntry(ctx context.Context, id int64) (db.JournalEntry, error) {
	var out db.JournalEntry
	if id <= 0 {
		return out, errors.New("journal entry id must be positive")
	}
	err := c.do(ctx, "GET", "/api/journal/"+strconv.FormatInt(id, 10), nil, &out)
	return out, err
}

func (c *Client) AppendNote(ctx context.Context, owner db.JournalOwner, in db.AppendNoteInput) (db.JournalEntry, error) {
	var out db.JournalEntry
	path, err := journalPath(owner)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, "POST", path, in, &out)
	return out, err
}
