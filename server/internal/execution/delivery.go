package execution

import "fmt"

// Delivery describes how an Interactive adapter delivers a message.
type Delivery string

const (
	DeliverySend  Delivery = "send"  // start a turn after the previous turn has ended; also the default
	DeliverySteer Delivery = "steer" // inject into the running turn
	DeliveryQueue Delivery = "queue" // queue natively for the turn after the running one
)

// Operation validates d and returns the operation that gates it.
func (d Delivery) Operation() (Op, error) {
	switch d {
	case "", DeliverySend:
		return OpSend, nil
	case DeliverySteer:
		return OpSteer, nil
	case DeliveryQueue:
		return OpQueue, nil
	default:
		return "", &Invalid{Reason: fmt.Sprintf("unknown message delivery %q; use send, steer or queue", d)}
	}
}

// RequireSend rejects every delivery besides send, including unknown values.
func (d Delivery) RequireSend() error {
	op, err := d.Operation()
	if err != nil {
		return err
	}
	if op != OpSend {
		return &Invalid{Reason: fmt.Sprintf("message delivery %q is unsupported here", d)}
	}
	return nil
}
