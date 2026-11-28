package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const inboxContractPath = "/api/session/{sessionID}/inbox"
const queueManageUnavailable = "Update OpenCode to manage queued messages in Stage."

func inspectQueueManage(ops map[string]operation, schemas map[string]json.RawMessage) bool {
	update := ops["patch"]
	return ops["delete"].OperationID == "session.inbox.cancel" &&
		update.OperationID == "session.inbox.update" &&
		inspectDelivery(update, schemas)[string(execution.DeliverySteer)]
}

type inboxMessage struct {
	ID, Type, SessionID string
	Time                wireTime
	Delivery            execution.Delivery
}

type inboxUserPayload struct {
	Text     string
	Metadata map[string]interface{}
}

type admitted struct {
	inboxMessage
	Payload inboxUserPayload
}

func inboxPath(sessionID string) string {
	return "/api/session/" + url.PathEscape(sessionID) + "/inbox"
}

// userInbox also serves launch and send recovery. Those callers identify
// admissions by their markers, including on older services without delivery.
func (a *Adapter) userInbox(ctx context.Context, sessionID string) ([]admitted, error) {
	var inbox envelope[[]struct {
		inboxMessage
		Payload json.RawMessage
	}]
	if err := a.get(ctx, inboxPath(sessionID), &inbox); err != nil {
		return nil, err
	}
	messages := make([]admitted, 0, len(inbox.Data))
	for _, item := range inbox.Data {
		// The same inbox carries system entries, including non-message payloads.
		if item.Type != "user" || item.SessionID != sessionID || len(item.Payload) == 0 {
			continue
		}
		message := admitted{inboxMessage: item.inboxMessage}
		if err := json.Unmarshal(item.Payload, &message.Payload); err != nil {
			return nil, fmt.Errorf("invalid OpenCode inbox payload: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (a *Adapter) Queued(ctx context.Context, ref execution.SessionRef) ([]execution.QueuedMessage, error) {
	if !a.valid(ref) {
		return nil, execution.ErrNotFound
	}
	contract, err := a.checkContract(ctx)
	if err != nil {
		return nil, err
	}
	if !contract.queueManage {
		return nil, errors.New(queueManageUnavailable)
	}
	return a.pendingMessages(ctx, ref.ExternalSessionID)
}

func (a *Adapter) pendingMessages(ctx context.Context, sessionID string) ([]execution.QueuedMessage, error) {
	inbox, err := a.userInbox(ctx, sessionID)
	if err != nil {
		return nil, sessionReadErr(err)
	}
	messages := make([]execution.QueuedMessage, 0, len(inbox))
	for _, message := range inbox {
		if message.ID == "" || message.Delivery != execution.DeliveryQueue && message.Delivery != execution.DeliverySteer {
			continue
		}
		source := "harness"
		stageMessage, _ := message.Payload.Metadata["stageMessage"].(string)
		stageAttempt, _ := message.Payload.Metadata["stageAttempt"].(string)
		if stageMessage != "" || stageAttempt != "" {
			source = "stage"
		}
		messages = append(messages, execution.QueuedMessage{
			ID: message.ID, Text: message.Payload.Text, Delivery: message.Delivery,
			CreatedAt: time.UnixMilli(message.Time.Created).UTC(), Source: source,
		})
	}
	return messages, nil
}

func (a *Adapter) CancelQueued(ctx context.Context, ref execution.SessionRef, id string) error {
	return a.changeQueued(ctx, ref, id, http.MethodDelete, nil)
}

func (a *Adapter) PromoteQueued(ctx context.Context, ref execution.SessionRef, id string) error {
	return a.changeQueued(ctx, ref, id, http.MethodPatch, map[string]execution.Delivery{"delivery": execution.DeliverySteer})
}

func (a *Adapter) changeQueued(ctx context.Context, ref execution.SessionRef, id, method string, body interface{}) error {
	match := func(message execution.QueuedMessage) bool { return message.ID == id }
	messages, err := a.Queued(ctx, ref)
	if err != nil {
		return err
	}
	// OpenCode acknowledges a DELETE even when the item was already gone.
	if !slices.ContainsFunc(messages, match) {
		return execution.ErrNotFound
	}
	err = a.request(ctx, method, inboxPath(ref.ExternalSessionID)+"/"+url.PathEscape(id), body, nil)
	var status responseError
	if method == http.MethodPatch && errors.As(err, &status) && status.code == http.StatusConflict {
		// A PATCH conflict can mean the item was consumed after the first read.
		pending, readErr := a.pendingMessages(ctx, ref.ExternalSessionID)
		if readErr != nil {
			return readErr
		}
		if !slices.ContainsFunc(pending, match) {
			return execution.ErrNotFound
		}
	}
	return sessionReadErr(err)
}
